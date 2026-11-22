package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	gh "github.com/google/go-github/v93/github"
	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/config"
	"github.com/mubarak-j/tf-plan-cost/internal/github"
	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/resources"
	"github.com/mubarak-j/tf-plan-cost/internal/render"
)

// PlanResponse is the JSON answer to a plan upload. The Action maps it to
// the same outputs as Action mode.
type PlanResponse struct {
	DeltaMonthlyUSD       decimal.Decimal `json:"delta_monthly_usd"`
	TotalBeforeMonthlyUSD decimal.Decimal `json:"total_before_monthly_usd"`
	TotalAfterMonthlyUSD  decimal.Decimal `json:"total_after_monthly_usd"`
	Exceeded              bool            `json:"exceeded"`
	CheckURL              string          `json:"check_url"`
	CommentURL            string          `json:"comment_url,omitempty"`
	// Stale is true when the pull request has moved on since the plan's
	// commit: the check run was created on that commit, but the comment was
	// left for the newer run.
	Stale bool `json:"stale,omitempty"`
	// Report is the Markdown report, for the workflow's job summary.
	Report string `json:"report"`
}

var pullRef = regexp.MustCompile(`^refs/pull/([0-9]+)/merge$`)

// uploadOptions are the optional query parameters of POST /v1/plans.
type uploadOptions struct {
	commentID         string
	region            string
	configPath        string
	comment           bool
	commentOnNoChange bool
}

func parseUploadOptions(r *http.Request) (uploadOptions, error) {
	q := r.URL.Query()
	opts := uploadOptions{
		commentID:  q.Get("comment_id"),
		region:     q.Get("region"),
		configPath: q.Get("config"),
		comment:    true,
	}
	if opts.commentID == "" {
		opts.commentID = render.DefaultCommentID
	}
	if !render.ValidCommentID(opts.commentID) {
		return opts, errors.New("invalid comment_id: use 1-64 characters from A-Z a-z 0-9 . _ / -")
	}
	if opts.configPath == "" {
		opts.configPath = config.DefaultPath
	}
	if clean := path.Clean(opts.configPath); clean != opts.configPath || strings.HasPrefix(clean, "/") ||
		clean == ".." || strings.HasPrefix(clean, "../") {
		return opts, errors.New("invalid config: use a relative path inside the repository")
	}
	if opts.region != "" && !validRegion(opts.region) {
		return opts, errors.New("invalid region")
	}
	for name, dst := range map[string]*bool{"comment": &opts.comment, "comment_on_no_change": &opts.commentOnNoChange} {
		if v := q.Get(name); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return opts, fmt.Errorf("invalid %s: want true or false", name)
			}
			*dst = b
		}
	}
	return opts, nil
}

var regionRE = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

func validRegion(s string) bool { return len(s) <= 32 && regionRE.MatchString(s) }

// handlePlan prices a plan uploaded from a pull_request workflow run and
// reports it as a Check Run and a pull request comment. The repository and
// pull request come only from the run's verified OIDC token.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := s.logger()

	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token: send the GitHub Actions OIDC token with audience tf-plan-cost")
		return
	}
	claims, err := s.Verifier.Verify(ctx, raw)
	if err != nil {
		log.Info("plan upload: token rejected", "err", err)
		writeError(w, http.StatusUnauthorized, "invalid token: send a GitHub Actions OIDC token with audience tf-plan-cost")
		return
	}
	log = log.With("repo", claims.Repository, "run_id", claims.RunID, "ref", claims.Ref)

	m := pullRef.FindStringSubmatch(claims.Ref)
	if m == nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf(
			"tf-plan-cost App mode runs on pull_request events (got event %q, ref %q)", claims.EventName, claims.Ref))
		return
	}
	pr, _ := strconv.Atoi(m[1])
	log = log.With("pr", pr)

	opts, err := parseUploadOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	body := http.MaxBytesReader(w, r.Body, s.maxBody())
	p, err := plan.Parse(body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("plan is larger than %d bytes; upload the output of `tf-plan-cost strip`", s.maxBody()))
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid plan: "+err.Error())
		return
	}

	client, err := s.GitHub.RepoClient(ctx, claims.Owner(), claims.Name(), claims.RepoID())
	if errors.Is(err, github.ErrNotInstalled) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("the tf-plan-cost App is not installed on %s", claims.Repository))
		return
	}
	if err != nil {
		log.Error("plan upload: installation client", "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error")
		return
	}
	owner, repo := claims.Owner(), claims.Name()

	head, stale, err := planHead(ctx, client, owner, repo, claims.SHA, pr)
	if err != nil {
		var ue userError
		if errors.As(err, &ue) {
			writeError(w, http.StatusUnprocessableEntity, ue.Error())
			return
		}
		log.Error("plan upload: resolve head commit", "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error")
		return
	}

	cfg, err := loadConfig(ctx, client, owner, repo, head, opts.configPath)
	if err != nil {
		var ue userError
		if errors.As(err, &ue) {
			writeError(w, http.StatusUnprocessableEntity, ue.Error())
			return
		}
		log.Error("plan upload: read config", "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error")
		return
	}

	est := pricing.Estimator{Client: s.Prices, Pricers: resources.Registry(), DefaultRegion: opts.region}
	report, err := est.Estimate(ctx, p)
	if err != nil {
		log.Error("plan upload: pricing", "err", err)
		writeError(w, http.StatusBadGateway, "AWS pricing error")
		return
	}

	var md bytes.Buffer
	ropts := render.Options{CommentID: opts.commentID, FailAbove: cfg.FailAboveMonthlyUSD, LimitSource: opts.configPath}
	if err := render.Markdown(&md, report, ropts); err != nil {
		log.Error("plan upload: render", "err", err)
		writeError(w, http.StatusInternalServerError, "render report")
		return
	}

	exceeded := cfg.Exceeded(report.Delta)
	checks := &github.Checks{Client: client, Owner: owner, Repo: repo}
	checkURL, err := checks.Create(ctx, github.CheckRun{
		Name:       checkName(opts.commentID),
		HeadSHA:    head,
		Failed:     exceeded,
		Title:      render.Headline(report),
		Summary:    md.String(),
		ExternalID: claims.RunID,
		DetailsURL: fmt.Sprintf("%s/%s/actions/runs/%s", s.webURL(), claims.Repository, claims.RunID),
	})
	if err != nil {
		log.Error("plan upload: create check run", "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error: could not create the check run (does the App have Checks: write?)")
		return
	}

	resp := PlanResponse{
		DeltaMonthlyUSD:       report.Delta,
		TotalBeforeMonthlyUSD: report.TotalBefore,
		TotalAfterMonthlyUSD:  report.TotalAfter,
		Exceeded:              exceeded,
		CheckURL:              checkURL,
		Stale:                 stale,
		Report:                md.String(),
	}
	if opts.comment && !stale {
		c := &github.Commenter{Client: client, Owner: owner, Repo: repo, Author: s.BotLogin}
		res, err := c.Upsert(ctx, pr, render.Marker(opts.commentID), md.String(), report.HasChanges() || opts.commentOnNoChange)
		if err != nil {
			log.Error("plan upload: comment", "err", err)
			writeError(w, http.StatusBadGateway, "GitHub API error: the check run was created but the comment failed")
			return
		}
		resp.CommentURL = res.URL
	}

	log.Info("plan priced", slog.String("delta", report.Delta.StringFixed(2)), slog.Bool("exceeded", exceeded),
		slog.Bool("stale", stale), slog.String("head", head))
	writeJSON(w, http.StatusOK, resp)
}

// checkName gives each Terraform root its own check, like its own comment.
func checkName(commentID string) string {
	if commentID == render.DefaultCommentID {
		return github.CheckName
	}
	return github.CheckName + " / " + commentID
}

// userError is a problem with the user's repository or workflow, reported
// as 422 with its message.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

// planHead returns the pull request head commit the plan was built from and
// whether the pull request has moved past it. On pull_request runs the
// token's sha is GitHub's test merge commit, whose second parent is the head.
func planHead(ctx context.Context, client *gh.Client, owner, repo, mergeSHA string, pr int) (head string, stale bool, err error) {
	commit, _, err := client.Repositories.GetCommit(ctx, owner, repo, mergeSHA, nil)
	if github.IsNotFound(err) || github.IsStatus(err, http.StatusUnprocessableEntity) {
		return "", false, userError{fmt.Sprintf("commit %s from the token was not found", mergeSHA)}
	}
	if err != nil {
		return "", false, fmt.Errorf("get commit %s: %w", mergeSHA, err)
	}
	if len(commit.Parents) != 2 {
		return "", false, userError{fmt.Sprintf("commit %s is not a pull request merge commit", mergeSHA)}
	}
	head = commit.Parents[1].GetSHA()

	pull, _, err := client.PullRequests.Get(ctx, owner, repo, pr)
	if github.IsNotFound(err) {
		return "", false, userError{fmt.Sprintf("pull request #%d was not found", pr)}
	}
	if err != nil {
		return "", false, fmt.Errorf("get pull request #%d: %w", pr, err)
	}
	return head, pull.GetHead().GetSHA() != head, nil
}

// loadConfig reads the config file at the plan's head commit. A missing
// file means no limit.
func loadConfig(ctx context.Context, client *gh.Client, owner, repo, ref, filePath string) (*config.Config, error) {
	file, _, _, err := client.Repositories.GetContents(ctx, owner, repo, filePath, &gh.RepositoryContentGetOptions{Ref: ref})
	if github.IsNotFound(err) {
		return &config.Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", filePath, err)
	}
	if file == nil {
		return nil, userError{filePath + " is a directory"}
	}
	content, err := file.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filePath, err)
	}
	cfg, err := config.Parse([]byte(content))
	if err != nil {
		return nil, userError{fmt.Sprintf("%s: %v", filePath, err)}
	}
	return cfg, nil
}
