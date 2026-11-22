package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mubarak-j/tf-plan-cost/internal/app"
	"github.com/mubarak-j/tf-plan-cost/internal/config"
	ghpost "github.com/mubarak-j/tf-plan-cost/internal/github"
	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/render"
)

// runUpload strips a plan, authenticates with the workflow's OIDC token and
// sends the plan to the tf-plan-cost App, which prices it and posts the
// Check Run and comment. The Check Run is the cost gate, so this command
// does not fail when the limit is exceeded.
func runUpload(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, httpClient *http.Client) int {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	appURL := fs.String("app-url", "", "tf-plan-cost App server URL (required)")
	defaultRegion := getenv("AWS_REGION")
	if defaultRegion == "" {
		defaultRegion = getenv("AWS_DEFAULT_REGION")
	}
	region := fs.String("region", defaultRegion, "AWS region for resources whose region is not in the plan (default $AWS_REGION or $AWS_DEFAULT_REGION)")
	commentID := fs.String("comment-id", render.DefaultCommentID, "separate comment and check per Terraform root, e.g. prod")
	configPath := fs.String("config", config.DefaultPath, "config file path in the repository")
	comment := fs.Bool("comment", true, "create or update the pull request comment")
	commentOnNoChange := fs.Bool("comment-on-no-change", false, "post a comment even when no resources change")
	audience := fs.String("audience", app.DefaultAudience, "OIDC token audience")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: tf-plan-cost upload --app-url URL [flags] plan.json

Runs in a GitHub Actions pull_request workflow with permissions id-token: write.
Removes sensitive values from the plan, then sends it to the tf-plan-cost App
with the workflow's OIDC token. Writes the job summary and step outputs.

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
	if len(positional) != 1 || *appURL == "" {
		fs.Usage()
		return 2
	}
	endpoint, err := url.Parse(strings.TrimSuffix(*appURL, "/") + "/v1/plans")
	if err != nil || (endpoint.Scheme != "https" && endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "localhost") {
		fmt.Fprintf(stderr, "invalid --app-url %q: use an https URL\n", *appURL)
		return 2
	}
	if !render.ValidCommentID(*commentID) {
		fmt.Fprintf(stderr, "invalid --comment-id %q: use 1-64 characters from A-Z a-z 0-9 . _ / -\n", *commentID)
		return 2
	}

	var stripped bytes.Buffer
	if err := stripFile(positional[0], &stripped); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	token, err := actionsIDToken(ctx, httpClient, getenv, *audience)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	q := url.Values{}
	q.Set("comment_id", *commentID)
	q.Set("config", *configPath)
	q.Set("comment", strconv.FormatBool(*comment))
	q.Set("comment_on_no_change", strconv.FormatBool(*commentOnNoChange))
	if *region != "" {
		q.Set("region", *region)
	}
	endpoint.RawQuery = q.Encode()

	resp, err := postPlan(ctx, httpClient, endpoint.String(), token, stripped.Bytes())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	env := ghpost.ActionsEnv{StepSummary: getenv("GITHUB_STEP_SUMMARY"), Output: getenv("GITHUB_OUTPUT")}
	if err := env.AppendStepSummary(resp.Report); err != nil {
		fmt.Fprintln(stderr, "warning: write job summary:", err)
	}
	if err := env.SetOutputs([][2]string{
		{"delta-monthly-usd", resp.DeltaMonthlyUSD.StringFixed(2)},
		{"total-before-monthly-usd", resp.TotalBeforeMonthlyUSD.StringFixed(2)},
		{"total-after-monthly-usd", resp.TotalAfterMonthlyUSD.StringFixed(2)},
		{"exceeded", strconv.FormatBool(resp.Exceeded)},
		{"comment-url", resp.CommentURL},
		{"check-url", resp.CheckURL},
	}); err != nil {
		fmt.Fprintln(stderr, "warning: write step outputs:", err)
	}

	fmt.Fprintf(stdout, "Check run: %s\n", resp.CheckURL)
	switch {
	case resp.Stale:
		fmt.Fprintln(stdout, "::notice title=tf-plan-cost::The pull request has newer commits; the comment was left for the newer run.")
	case resp.CommentURL != "":
		fmt.Fprintf(stdout, "Comment: %s\n", resp.CommentURL)
	}
	if resp.Exceeded {
		fmt.Fprintf(stdout, "::warning title=tf-plan-cost::Monthly cost increase of $%s is above the limit; the tf-plan-cost check fails.\n",
			resp.DeltaMonthlyUSD.StringFixed(2))
	}
	return 0
}

func stripFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := plan.Strip(f, w); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// actionsIDToken requests the workflow's OIDC token for audience.
func actionsIDToken(ctx context.Context, client *http.Client, getenv func(string) string, audience string) (string, error) {
	reqURL, reqToken := getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return "", errors.New("no GitHub Actions OIDC token available: add `permissions: id-token: write` to the job " +
			"(pull requests from forks never get one)")
	}
	u, err := url.Parse(reqURL)
	if err != nil {
		return "", fmt.Errorf("ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+reqToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request OIDC token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request OIDC token: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.Value == "" {
		return "", errors.New("request OIDC token: unexpected response")
	}
	return body.Value, nil
}

// postPlan uploads the plan, retrying once on a network error or 5xx (for
// example a cold start).
func postPlan(ctx context.Context, client *http.Client, endpoint, token string, plan []byte) (*app.PlanResponse, error) {
	var lastErr error
	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(plan))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("upload plan: %w", err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("upload plan: %w", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(data, &e)
			if e.Error == "" {
				e.Error = http.StatusText(resp.StatusCode)
			}
			lastErr = fmt.Errorf("tf-plan-cost App: HTTP %d: %s", resp.StatusCode, e.Error)
			if resp.StatusCode >= 500 {
				continue
			}
			return nil, lastErr
		}
		var out app.PlanResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("tf-plan-cost App: unexpected response: %w", err)
		}
		return &out, nil
	}
	return nil, lastErr
}
