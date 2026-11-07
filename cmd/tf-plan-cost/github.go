package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mubarak-j/tf-plan-cost/internal/config"
	ghpost "github.com/mubarak-j/tf-plan-cost/internal/github"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/render"
)

// exitLimitExceeded is returned when the cost increase is above the
// configured limit, so CI fails the step.
const exitLimitExceeded = 3

// actionsBotLogin authors comments made with a workflow's GITHUB_TOKEN.
const actionsBotLogin = "github-actions[bot]"

// runGitHubComment posts a JSON report (from `diff --format json`) as a pull
// request comment, writes the job summary and step outputs, and enforces the
// cost limit from the config file.
func runGitHubComment(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("github-comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reportPath := fs.String("report", "", "JSON report from `tf-plan-cost diff --format json` (required)")
	commentID := fs.String("comment-id", render.DefaultCommentID, "separate comment per Terraform root, e.g. prod")
	configPath := fs.String("config", config.DefaultPath, "config file with fail_above_monthly_usd (optional)")
	comment := fs.Bool("comment", true, "create or update the pull request comment")
	commentOnNoChange := fs.Bool("comment-on-no-change", false, "post a comment even when no resources change")
	prFlag := fs.Int("pr", 0, "pull request number (default: from the GitHub Actions event)")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: tf-plan-cost github-comment --report report.json [flags]

Runs in GitHub Actions. Reads GITHUB_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_PATH,
GITHUB_API_URL, GITHUB_STEP_SUMMARY and GITHUB_OUTPUT. Exits 3 when the cost
increase is above fail_above_monthly_usd.

Flags:
`)
		fs.PrintDefaults()
	}
	positional, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if len(positional) != 0 || *reportPath == "" {
		fs.Usage()
		return 2
	}
	if !render.ValidCommentID(*commentID) {
		fmt.Fprintf(stderr, "invalid --comment-id %q: use 1-64 characters from A-Z a-z 0-9 . _ / -\n", *commentID)
		return 2
	}

	report, err := readReport(*reportPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	var md bytes.Buffer
	opts := render.Options{CommentID: *commentID, FailAbove: cfg.FailAboveMonthlyUSD, LimitSource: *configPath}
	if err := render.Markdown(&md, report, opts); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	env := ghpost.ActionsEnvFromOS()
	if err := env.AppendStepSummary(md.String()); err != nil {
		fmt.Fprintln(stderr, "warning: write job summary:", err)
	}

	var commentURL string
	var commentErr error
	if *comment {
		commentURL, commentErr = postComment(ctx, env, *prFlag, render.Marker(*commentID), md.String(),
			report.HasChanges() || *commentOnNoChange, stdout)
	}

	exceeded := cfg.Exceeded(report.Delta)
	if err := env.SetOutputs([][2]string{
		{"delta-monthly-usd", report.Delta.StringFixed(2)},
		{"total-before-monthly-usd", report.TotalBefore.StringFixed(2)},
		{"total-after-monthly-usd", report.TotalAfter.StringFixed(2)},
		{"exceeded", fmt.Sprint(exceeded)},
		{"comment-url", commentURL},
	}); err != nil {
		fmt.Fprintln(stderr, "warning: write step outputs:", err)
	}

	if commentErr != nil {
		fmt.Fprintln(stderr, "error:", commentErr)
		return 1
	}
	if exceeded {
		// Workflow command: shows as an annotation on the run.
		fmt.Fprintf(stdout, "::error title=tf-plan-cost::Monthly cost increase of $%s is above the $%s limit in %s\n",
			report.Delta.StringFixed(2), cfg.FailAboveMonthlyUSD.StringFixed(2), *configPath)
		return exitLimitExceeded
	}
	return 0
}

// postComment creates or updates the PR comment. Situations where commenting
// is impossible but expected (not a PR, fork PR) are warnings, not errors.
func postComment(ctx context.Context, env ghpost.ActionsEnv, prNumber int, marker, body string, create bool, stdout io.Writer) (string, error) {
	if prNumber == 0 {
		pr, err := env.PullRequest()
		if err != nil {
			return "", err
		}
		if pr == nil {
			fmt.Fprintln(stdout, "Not a pull request event; skipping the comment (see the job summary).")
			return "", nil
		}
		if pr.Fork {
			fmt.Fprintln(stdout, "::warning title=tf-plan-cost::Pull requests from forks get a read-only token; skipping the comment (see the job summary).")
			return "", nil
		}
		prNumber = pr.Number
	}
	if env.Token == "" {
		return "", errors.New("GITHUB_TOKEN is not set; pass github-token to the action or use --comment=false")
	}
	owner, repo, err := env.OwnerRepo()
	if err != nil {
		return "", err
	}
	client, err := ghpost.NewClient(env.Token, env.APIURL)
	if err != nil {
		return "", err
	}
	// Match only our own earlier comment. A personal access token can read
	// its login; installation tokens (GITHUB_TOKEN) are the Actions bot.
	author, err := ghpost.TokenLogin(ctx, client)
	if err != nil {
		author = actionsBotLogin
	}
	c := &ghpost.Commenter{Client: client, Owner: owner, Repo: repo, Author: author}
	res, err := c.Upsert(ctx, prNumber, marker, body, create)
	if ghpost.IsForbidden(err) {
		fmt.Fprintln(stdout, "::warning title=tf-plan-cost::The token cannot comment on this pull request (it needs pull-requests: write); see the job summary instead.")
		return "", nil
	}
	if err != nil {
		return "", err
	}
	switch res.Outcome {
	case ghpost.Skipped:
		fmt.Fprintln(stdout, "No resource changes and no earlier comment; not commenting.")
	default:
		fmt.Fprintf(stdout, "Comment %s: %s\n", res.Outcome, res.URL)
	}
	return res.URL, nil
}

func readReport(path string) (*pricing.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r pricing.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: not a tf-plan-cost JSON report: %w", path, err)
	}
	return &r, nil
}
