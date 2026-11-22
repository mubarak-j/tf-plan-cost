package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mubarak-j/tf-plan-cost/internal/github"
)

func TestValidSignature(t *testing.T) {
	body := []byte(`{"zen":"hi"}`)
	tests := []struct {
		name   string
		secret string
		header string
		want   bool
	}{
		{"valid", testSecret, sign(body), true},
		{"wrong secret", "other", sign(body), false},
		{"missing header", testSecret, "", false},
		{"sha1 header", testSecret, "sha1=abc", false},
		{"not hex", testSecret, "sha256=zz", false},
		{"no secret configured", "", sign(body), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSignature([]byte(tt.secret), body, tt.header); got != tt.want {
				t.Errorf("validSignature = %v, want %v", got, tt.want)
			}
		})
	}
	if validSignature([]byte(testSecret), []byte(`{"zen":"tampered"}`), sign(body)) {
		t.Error("tampered body accepted")
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	h := newHarness(t, nil)
	code, _ := h.do(http.MethodPost, "/webhook", map[string]string{
		"X-GitHub-Event": "ping", "X-Hub-Signature-256": "sha256=00",
	}, []byte(`{}`))
	if code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

func TestWebhookPing(t *testing.T) {
	h := newHarness(t, nil)
	if code, body := h.webhook("ping", []byte(`{"zen":"Keep it logically awesome."}`)); code != http.StatusOK || !strings.Contains(body, "pong") {
		t.Errorf("ping = %d %s", code, body)
	}
}

func TestWebhookUnknownEvent(t *testing.T) {
	h := newHarness(t, nil)
	if code, _ := h.webhook("pull_request", []byte(`{"action":"opened"}`)); code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", code)
	}
}

func TestWebhookTooLarge(t *testing.T) {
	h := newHarness(t, nil)
	h.server.MaxBodyBytes = 10
	if code, _ := h.webhook("ping", []byte(`{"zen":"this is longer than ten bytes"}`)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", code)
	}
}

func checkRunEvent(action string, appID, checkID int64, summary string) []byte {
	return fmt.Appendf(nil, `{
  "action": %q,
  "check_run": {
    "id": %d, "name": "tf-plan-cost", "head_sha": "head-sha",
    "details_url": "https://github.com/o/r/actions/runs/555",
    "app": {"id": %d},
    "output": {"title": "+$1.00/mo", "summary": %q}
  },
  "repository": {"id": 42, "name": "r", "full_name": "o/r", "owner": {"login": "o"}},
  "installation": {"id": 7}
}`, action, checkID, appID, summary)
}

func TestWebhookCheckRunRerequested(t *testing.T) {
	h := newHarness(t, nil)
	client, err := h.server.GitHub.RepoClient(t.Context(), "o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	checks := &github.Checks{Client: client, Owner: "o", Repo: "r"}
	if _, err := checks.Create(t.Context(), github.CheckRun{HeadSHA: headSHA, Title: "+$1.00/mo", Summary: "report"}); err != nil {
		t.Fatal(err)
	}
	id := h.gh.Checks()[0].ID

	if code, body := h.webhook("check_run", checkRunEvent("rerequested", testAppID, id, "report")); code != http.StatusNoContent {
		t.Fatalf("status = %d %s", code, body)
	}
	got := h.gh.Checks()[0].Output.Summary
	if !strings.HasPrefix(got, "**To re-run") || !strings.Contains(got, "actions/runs/555") {
		t.Errorf("summary = %q", got)
	}
}

func TestWebhookCheckRunIgnored(t *testing.T) {
	h := newHarness(t, nil)
	for name, body := range map[string][]byte{
		"completed":       checkRunEvent("completed", testAppID, 1, ""),
		"other App's run": checkRunEvent("rerequested", 999, 1, ""),
	} {
		t.Run(name, func(t *testing.T) {
			// No GitHub requests are expected: the fake fails the test on any.
			if code, _ := h.webhook("check_run", body); code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", code)
			}
		})
	}
	if code, _ := h.webhook("check_run", []byte(`not json`)); code != http.StatusBadRequest {
		t.Errorf("invalid payload: status = %d, want 400", code)
	}
}
