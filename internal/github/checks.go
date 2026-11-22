package github

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	gh "github.com/google/go-github/v93/github"
)

// CheckName is the name of tf-plan-cost's Check Run.
const CheckName = "tf-plan-cost"

// maxCheckText is GitHub's limit for a check run's output summary and text.
const maxCheckText = 65535

// CheckRun is a completed tf-plan-cost Check Run.
type CheckRun struct {
	// Name defaults to CheckName.
	Name    string
	HeadSHA string
	// Failed sets the conclusion to failure instead of success.
	Failed bool
	// Title is a one-line result, e.g. "+$242.24/mo".
	Title string
	// Summary is Markdown; it is truncated to fit GitHub's limit.
	Summary string
	// ExternalID and DetailsURL point to the workflow run that sent the plan.
	ExternalID string
	DetailsURL string
}

// Checks creates Check Runs on one repository.
type Checks struct {
	Client      *gh.Client
	Owner, Repo string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Create posts a completed check run and returns its web URL.
func (c *Checks) Create(ctx context.Context, run CheckRun) (string, error) {
	name := run.Name
	if name == "" {
		name = CheckName
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ts := &gh.Timestamp{Time: now()}
	opts := gh.CreateCheckRunOptions{
		Name:        name,
		HeadSHA:     run.HeadSHA,
		Status:      new("completed"),
		Conclusion:  new(conclusion(run.Failed)),
		StartedAt:   ts,
		CompletedAt: ts,
		Output: &gh.CheckRunOutput{
			Title:   new(run.Title),
			Summary: new(Truncate(run.Summary, maxCheckText)),
		},
	}
	if run.ExternalID != "" {
		opts.ExternalID = new(run.ExternalID)
	}
	if run.DetailsURL != "" {
		opts.DetailsURL = new(run.DetailsURL)
	}
	cr, _, err := c.Client.Checks.CreateCheckRun(ctx, c.Owner, c.Repo, opts)
	if err != nil {
		return "", fmt.Errorf("create check run on %s: %w", run.HeadSHA, err)
	}
	return cr.GetHTMLURL(), nil
}

// AddRerunHint updates an existing check run's output to explain that
// tf-plan-cost cannot re-run by itself: the plan has to be uploaded again by
// re-running the workflow at detailsURL.
func (c *Checks) AddRerunHint(ctx context.Context, cr *gh.CheckRun) error {
	out := cr.GetOutput()
	hint := "**To re-run tf-plan-cost, re-run the workflow that produced the plan"
	if u := cr.GetDetailsURL(); u != "" {
		hint += ": " + u
	}
	hint += ".** tf-plan-cost prices the plan your workflow uploads and does not keep a copy."
	summary := out.GetSummary()
	if strings.HasPrefix(summary, "**To re-run") {
		return nil // already added
	}
	summary = hint + "\n\n" + summary
	_, _, err := c.Client.Checks.UpdateCheckRun(ctx, c.Owner, c.Repo, cr.GetID(), gh.UpdateCheckRunOptions{
		Name: cr.GetName(),
		Output: &gh.CheckRunOutput{
			Title:   new(out.GetTitle()),
			Summary: new(Truncate(summary, maxCheckText)),
		},
	})
	if err != nil {
		return fmt.Errorf("update check run %d: %w", cr.GetID(), err)
	}
	return nil
}

func conclusion(failed bool) string {
	if failed {
		return "failure"
	}
	return "success"
}

const truncatedNote = "\n\n_Truncated: the full report is too long for a check run. See the pull request comment._\n"

// Truncate shortens s to at most limit bytes, cutting at the last line
// break (or at least at a UTF-8 character boundary), and says that it did.
func Truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	n := max(0, limit-len(truncatedNote))
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + truncatedNote
}
