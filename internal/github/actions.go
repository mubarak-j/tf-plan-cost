package github

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ActionsEnv is the GitHub Actions runtime environment.
type ActionsEnv struct {
	Repository  string // GITHUB_REPOSITORY, owner/repo
	EventPath   string // GITHUB_EVENT_PATH
	APIURL      string // GITHUB_API_URL
	StepSummary string // GITHUB_STEP_SUMMARY
	Output      string // GITHUB_OUTPUT
	Token       string // GITHUB_TOKEN
}

// ActionsEnvFromOS reads the environment variables GitHub Actions sets.
func ActionsEnvFromOS() ActionsEnv {
	return ActionsEnv{
		Repository:  os.Getenv("GITHUB_REPOSITORY"),
		EventPath:   os.Getenv("GITHUB_EVENT_PATH"),
		APIURL:      os.Getenv("GITHUB_API_URL"),
		StepSummary: os.Getenv("GITHUB_STEP_SUMMARY"),
		Output:      os.Getenv("GITHUB_OUTPUT"),
		Token:       os.Getenv("GITHUB_TOKEN"),
	}
}

// OwnerRepo splits GITHUB_REPOSITORY.
func (e ActionsEnv) OwnerRepo() (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(e.Repository, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY %q is not owner/repo", e.Repository)
	}
	return owner, repo, nil
}

// PullRequest is the pull request that triggered the workflow.
type PullRequest struct {
	Number int
	// Fork is true when the head branch lives in another repository; the
	// workflow's GITHUB_TOKEN is then read-only.
	Fork bool
}

// PullRequest reads the event payload. It returns nil for events that are
// not about a pull request (push, schedule, ...).
func (e ActionsEnv) PullRequest() (*PullRequest, error) {
	if e.EventPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(e.EventPath)
	if err != nil {
		return nil, fmt.Errorf("read event payload: %w", err)
	}
	var ev struct {
		PullRequest *struct {
			Number int `json:"number"`
			Head   struct {
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("decode event payload: %w", err)
	}
	if ev.PullRequest == nil || ev.PullRequest.Number == 0 {
		return nil, nil
	}
	pr := ev.PullRequest
	// A deleted fork has a null head repo; treat it as a fork.
	fork := pr.Head.Repo == nil || pr.Head.Repo.FullName != pr.Base.Repo.FullName
	return &PullRequest{Number: pr.Number, Fork: fork}, nil
}

// AppendStepSummary appends Markdown to the job summary, if available.
func (e ActionsEnv) AppendStepSummary(markdown string) error {
	if e.StepSummary == "" {
		return nil
	}
	return appendFile(e.StepSummary, markdown+"\n")
}

// SetOutputs writes step outputs, if available. Values must be single-line.
func (e ActionsEnv) SetOutputs(kv [][2]string) error {
	if e.Output == "" {
		return nil
	}
	var b strings.Builder
	for _, p := range kv {
		if strings.ContainsAny(p[0]+p[1], "\r\n") || strings.Contains(p[0], "=") {
			return fmt.Errorf("invalid output %q=%q", p[0], p[1])
		}
		fmt.Fprintf(&b, "%s=%s\n", p[0], p[1])
	}
	return appendFile(e.Output, b.String())
}

func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
