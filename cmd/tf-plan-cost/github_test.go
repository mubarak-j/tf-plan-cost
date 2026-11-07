package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// commentAPI is a minimal fake of the GitHub comment endpoints.
type commentAPI struct {
	mu        sync.Mutex
	existing  []map[string]any
	posted    []string // bodies of created comments
	patched   []string // bodies of updated comments
	forbidden bool
	calls     int
}

func (a *commentAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	w.Header().Set("Content-Type", "application/json")
	var req struct{ Body string }
	switch {
	case r.URL.Path == "/user":
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "Resource not accessible by integration"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7/comments":
		_ = json.NewEncoder(w).Encode(a.existing)
	case a.forbidden:
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "Resource not accessible by integration"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/7/comments":
		_ = json.NewDecoder(r.Body).Decode(&req)
		a.posted = append(a.posted, req.Body)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id": 101, "html_url": "https://github.com/o/r/pull/7#issuecomment-101"}`)
	case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/comments/55":
		_ = json.NewDecoder(r.Body).Decode(&req)
		a.patched = append(a.patched, req.Body)
		fmt.Fprint(w, `{"id": 55, "html_url": "https://github.com/o/r/pull/7#issuecomment-55"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message": "unexpected %s %s"}`, r.Method, r.URL.Path)
	}
}

type actionsRun struct {
	stdout, stderr string
	code           int
	outputs        string
	summary        string
}

// setupActions sets the GitHub Actions environment for one test.
func setupActions(t *testing.T, api *commentAPI, event string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	eventPath := filepath.Join(dir, "event.json")
	if err := os.WriteFile(eventPath, []byte(event), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	t.Setenv("GITHUB_OUTPUT", filepath.Join(dir, "output"))
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(dir, "summary"))
	return dir
}

func runActions(t *testing.T, dir string, args ...string) actionsRun {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"github-comment"}, args...), nil, &stdout, &stderr, nil)
	out, _ := os.ReadFile(filepath.Join(dir, "output"))
	summary, _ := os.ReadFile(filepath.Join(dir, "summary"))
	return actionsRun{stdout: stdout.String(), stderr: stderr.String(), code: code, outputs: string(out), summary: string(summary)}
}

const (
	prEvent   = `{"pull_request": {"number": 7, "head": {"repo": {"full_name": "o/r"}}, "base": {"repo": {"full_name": "o/r"}}}}`
	forkEvent = `{"pull_request": {"number": 7, "head": {"repo": {"full_name": "someone/r"}}, "base": {"repo": {"full_name": "o/r"}}}}`
	pushEvent = `{"ref": "refs/heads/main"}`
)

var (
	updateReport = filepath.Join(testdata, "golden", "update.json") // +$242.24/mo
	noopReport   = filepath.Join(testdata, "golden", "noop.json")
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, ".tf-plan-cost.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGitHubCommentCreates(t *testing.T) {
	api := &commentAPI{existing: []map[string]any{{"id": 1, "body": "LGTM", "user": map[string]string{"login": "alice"}}}}
	dir := setupActions(t, api, prEvent)
	cfg := writeConfig(t, dir, "fail_above_monthly_usd: 500\n")

	r := runActions(t, dir, "--report", updateReport, "--config", cfg)
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	if len(api.posted) != 1 {
		t.Fatalf("posted %d comments, want 1", len(api.posted))
	}
	body := api.posted[0]
	for _, want := range []string{"<!-- tf-plan-cost -->\n", "**+$242.24/mo**", "Within the $500.00/mo limit"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment missing %q:\n%s", want, body)
		}
	}
	wantOutputs := "delta-monthly-usd=242.24\ntotal-before-monthly-usd=323.57\ntotal-after-monthly-usd=565.81\n" +
		"exceeded=false\ncomment-url=https://github.com/o/r/pull/7#issuecomment-101\n"
	if r.outputs != wantOutputs {
		t.Errorf("outputs:\n%s\nwant:\n%s", r.outputs, wantOutputs)
	}
	if r.summary != body+"\n" {
		t.Errorf("job summary should match the comment, got:\n%s", r.summary)
	}
	if !strings.Contains(r.stdout, "Comment created: https://github.com/o/r/pull/7#issuecomment-101") {
		t.Errorf("stdout: %s", r.stdout)
	}
}

func TestGitHubCommentUpdatesOwnComment(t *testing.T) {
	api := &commentAPI{existing: []map[string]any{
		{"id": 54, "body": "<!-- tf-plan-cost -->\nquoted", "user": map[string]string{"login": "alice"}},
		{"id": 55, "body": "<!-- tf-plan-cost:prod -->\nold", "user": map[string]string{"login": "github-actions[bot]"}},
	}}
	dir := setupActions(t, api, prEvent)
	r := runActions(t, dir, "--report", updateReport, "--comment-id", "prod")
	if r.code != 0 || len(api.patched) != 1 || len(api.posted) != 0 {
		t.Fatalf("exit %d, patched %d, posted %d\n%s%s", r.code, len(api.patched), len(api.posted), r.stdout, r.stderr)
	}
	if !strings.HasPrefix(api.patched[0], "<!-- tf-plan-cost:prod -->\n") {
		t.Errorf("patched body: %s", api.patched[0])
	}
}

func TestGitHubCommentLimitExceeded(t *testing.T) {
	api := &commentAPI{}
	dir := setupActions(t, api, prEvent)
	cfg := writeConfig(t, dir, "fail_above_monthly_usd: 100\n")
	r := runActions(t, dir, "--report", updateReport, "--config", cfg)
	if r.code != exitLimitExceeded {
		t.Fatalf("exit %d, want %d", r.code, exitLimitExceeded)
	}
	if len(api.posted) != 1 || !strings.Contains(api.posted[0], "[!CAUTION]") {
		t.Errorf("the comment should still be posted with a caution: %v", api.posted)
	}
	if !strings.Contains(r.outputs, "exceeded=true") {
		t.Errorf("outputs: %s", r.outputs)
	}
	if !strings.Contains(r.stdout, "::error title=tf-plan-cost::Monthly cost increase of $242.24 is above the $100.00 limit") {
		t.Errorf("stdout: %s", r.stdout)
	}
}

func TestGitHubCommentSkips(t *testing.T) {
	tests := []struct {
		name       string
		api        *commentAPI
		event      string
		report     string
		args       []string
		wantStdout string
		wantCalls  bool
	}{
		{name: "push event", api: &commentAPI{}, event: pushEvent, report: updateReport, wantStdout: "Not a pull request event"},
		{name: "fork", api: &commentAPI{}, event: forkEvent, report: updateReport, wantStdout: "::warning title=tf-plan-cost::Pull requests from forks"},
		{name: "commenting disabled", api: &commentAPI{}, event: prEvent, report: updateReport, args: []string{"--comment=false"}},
		{name: "no change and no earlier comment", api: &commentAPI{}, event: prEvent, report: noopReport, wantStdout: "not commenting", wantCalls: true},
		{name: "read-only token", api: &commentAPI{forbidden: true}, event: prEvent, report: updateReport, wantStdout: "needs pull-requests: write", wantCalls: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupActions(t, tt.api, tt.event)
			r := runActions(t, dir, append([]string{"--report", tt.report}, tt.args...)...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s%s", r.code, r.stdout, r.stderr)
			}
			if len(tt.api.posted)+len(tt.api.patched) != 0 {
				t.Errorf("should not write comments")
			}
			if (tt.api.calls > 0) != tt.wantCalls {
				t.Errorf("API calls = %d", tt.api.calls)
			}
			if !strings.Contains(r.stdout, tt.wantStdout) {
				t.Errorf("stdout = %q, want %q", r.stdout, tt.wantStdout)
			}
			if r.summary == "" || !strings.Contains(r.outputs, "comment-url=\n") {
				t.Errorf("summary and outputs are still written: summary=%q outputs=%q", r.summary, r.outputs)
			}
		})
	}
}

func TestGitHubCommentNoChangeOptIn(t *testing.T) {
	api := &commentAPI{}
	dir := setupActions(t, api, prEvent)
	r := runActions(t, dir, "--report", noopReport, "--comment-on-no-change")
	if r.code != 0 || len(api.posted) != 1 || !strings.Contains(api.posted[0], "no cost change") {
		t.Fatalf("exit %d posted %v", r.code, api.posted)
	}
}

func TestGitHubCommentErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		token      string
		wantCode   int
		wantStderr string
	}{
		{name: "missing report flag", args: nil, token: "t", wantCode: 2, wantStderr: "Usage: tf-plan-cost github-comment"},
		{name: "bad comment id", args: []string{"--report", updateReport, "--comment-id", "a b"}, token: "t", wantCode: 2, wantStderr: "invalid --comment-id"},
		{name: "report not found", args: []string{"--report", "nope.json"}, token: "t", wantCode: 1, wantStderr: "nope.json"},
		{name: "not a report", args: []string{"--report", filepath.Join(testdata, "golden", "update.md")}, token: "t", wantCode: 1, wantStderr: "not a tf-plan-cost JSON report"},
		{name: "missing token", args: []string{"--report", updateReport}, token: "", wantCode: 1, wantStderr: "GITHUB_TOKEN is not set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupActions(t, &commentAPI{}, prEvent)
			t.Setenv("GITHUB_TOKEN", tt.token)
			r := runActions(t, dir, tt.args...)
			if r.code != tt.wantCode || !strings.Contains(r.stderr, tt.wantStderr) {
				t.Errorf("exit %d stderr %q, want %d %q", r.code, r.stderr, tt.wantCode, tt.wantStderr)
			}
		})
	}

	dir := setupActions(t, &commentAPI{}, prEvent)
	bad := writeConfig(t, dir, "fail_above: 1\n")
	if r := runActions(t, dir, "--report", updateReport, "--config", bad); r.code != 1 || !strings.Contains(r.stderr, "fail_above") {
		t.Errorf("bad config: exit %d stderr %q", r.code, r.stderr)
	}
}
