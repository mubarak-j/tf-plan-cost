package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

const testdata = "../../testdata"

func recordedPrices(t *testing.T) *pricingtest.Client {
	t.Helper()
	prices, err := pricingtest.Load(filepath.Join(testdata, "prices.json"))
	if err != nil {
		t.Fatal(err)
	}
	return prices
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// upload posts a plan with a token carrying claims (nil = valid defaults).
func (h *harness) upload(query string, claims map[string]any, body []byte) (int, string) {
	h.t.Helper()
	if claims == nil {
		claims = h.iss.Claims()
	}
	return h.do(http.MethodPost, "/v1/plans"+query, map[string]string{
		"Authorization": "Bearer " + h.iss.Sign(h.t, claims),
		"Content-Type":  "application/json",
	}, body)
}

func decodeResponse(t *testing.T, body string) PlanResponse {
	t.Helper()
	var resp PlanResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return resp
}

func TestUploadPricesAndReports(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	plan := readFixture(t, "plans/update.json")
	golden := string(readFixture(t, "golden/update.md"))

	code, body := h.upload("", nil, plan)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	resp := decodeResponse(t, body)
	if resp.Exceeded || resp.Stale || resp.CheckURL == "" || resp.CommentURL == "" || resp.DeltaMonthlyUSD.IsZero() {
		t.Errorf("response = %+v", resp)
	}

	checks := h.gh.Checks()
	if len(checks) != 1 {
		t.Fatalf("got %d check runs, want 1", len(checks))
	}
	cr := checks[0]
	if cr.Name != "tf-plan-cost" || cr.HeadSHA != headSHA || cr.Conclusion != "success" || cr.ExternalID != "555" ||
		cr.DetailsURL != "https://github.com/o/r/actions/runs/555" || !strings.HasSuffix(cr.Output.Title, "/mo") ||
		cr.Output.Summary != golden {
		t.Errorf("check run = %+v", cr)
	}

	comments := h.gh.AllComments()
	if len(comments) != 1 || comments[0].PR != 7 || comments[0].Body != golden || comments[0].Login != "tf-plan-cost[bot]" {
		t.Fatalf("comments = %+v", comments)
	}

	// A second upload updates the same comment.
	if code, body := h.upload("", nil, plan); code != http.StatusOK {
		t.Fatalf("second upload: %d %s", code, body)
	}
	if n := len(h.gh.AllComments()); n != 1 {
		t.Errorf("got %d comments after re-upload, want 1", n)
	}
	if n := len(h.gh.Checks()); n != 2 {
		t.Errorf("got %d check runs after re-upload, want 2", n)
	}

	// Plan contents never reach the logs.
	if strings.Contains(h.logs.String(), "aws_instance") {
		t.Errorf("logs contain plan contents:\n%s", h.logs.String())
	}
}

func TestUploadStrippedPlanSameResult(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	var stripped strings.Builder
	if err := stripFixture(t, "plans/create.json", &stripped); err != nil {
		t.Fatal(err)
	}
	if code, body := h.upload("", nil, []byte(stripped.String())); code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if got, want := h.gh.AllComments()[0].Body, string(readFixture(t, "golden/create.md")); got != want {
		t.Errorf("comment from stripped plan differs from golden:\n%s", got)
	}
}

func TestUploadLimitExceeded(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	h.gh.Repos["o/r"].Files[headSHA+":.tf-plan-cost.yml"] = "fail_above_monthly_usd: 1\n"

	code, body := h.upload("", nil, readFixture(t, "plans/create.json"))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if resp := decodeResponse(t, body); !resp.Exceeded {
		t.Errorf("exceeded = false")
	}
	if c := h.gh.Checks()[0].Conclusion; c != "failure" {
		t.Errorf("conclusion = %q, want failure", c)
	}
	if !strings.Contains(h.gh.AllComments()[0].Body, "above") {
		t.Errorf("comment does not mention the limit:\n%s", h.gh.AllComments()[0].Body)
	}
}

func TestUploadConfigPathAndCommentID(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	h.gh.Repos["o/r"].Files[headSHA+":infra/prod/.tf-plan-cost.yml"] = "fail_above_monthly_usd: 1\n"

	code, body := h.upload("?comment_id=prod&config=infra/prod/.tf-plan-cost.yml", nil, readFixture(t, "plans/create.json"))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	cr := h.gh.Checks()[0]
	if cr.Name != "tf-plan-cost / prod" || cr.Conclusion != "failure" {
		t.Errorf("check run = %s %s", cr.Name, cr.Conclusion)
	}
	if c := h.gh.AllComments()[0].Body; !strings.HasPrefix(c, "<!-- tf-plan-cost:prod -->\n") {
		t.Errorf("comment marker: %q", c[:40])
	}
}

func TestUploadStale(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	h.gh.SetPRHead("o/r", 7, "newer-sha")

	code, body := h.upload("", nil, readFixture(t, "plans/update.json"))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	resp := decodeResponse(t, body)
	if !resp.Stale || resp.CommentURL != "" {
		t.Errorf("response = %+v, want stale without comment", resp)
	}
	if checks := h.gh.Checks(); len(checks) != 1 || checks[0].HeadSHA != headSHA {
		t.Errorf("check runs = %+v, want one on the plan's commit", checks)
	}
	if n := len(h.gh.AllComments()); n != 0 {
		t.Errorf("stale upload commented (%d comments)", n)
	}
}

func TestUploadNoChanges(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	code, body := h.upload("", nil, readFixture(t, "plans/noop.json"))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if n := len(h.gh.AllComments()); n != 0 {
		t.Errorf("no-change plan commented")
	}
	if c := h.gh.Checks()[0]; c.Output.Title != "No cost change" || c.Conclusion != "success" {
		t.Errorf("check run = %+v", c)
	}

	if code, _ := h.upload("?comment_on_no_change=true", nil, readFixture(t, "plans/noop.json")); code != http.StatusOK {
		t.Fatal(code)
	}
	if n := len(h.gh.AllComments()); n != 1 {
		t.Errorf("comment_on_no_change: %d comments, want 1", n)
	}
}

func TestUploadCommentDisabled(t *testing.T) {
	h := newHarness(t, recordedPrices(t))
	if code, body := h.upload("?comment=false", nil, readFixture(t, "plans/update.json")); code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if n := len(h.gh.AllComments()); n != 0 {
		t.Errorf("comment=false commented")
	}
}

func TestUploadErrors(t *testing.T) {
	plan := readFixture(t, "plans/update.json")
	claims := func(mutate func(map[string]any)) func(h *harness) map[string]any {
		return func(h *harness) map[string]any {
			c := h.iss.Claims()
			mutate(c)
			return c
		}
	}
	tests := []struct {
		name   string
		query  string
		claims func(h *harness) map[string]any
		body   []byte
		setup  func(h *harness)
		want   int
	}{
		{name: "wrong audience", claims: claims(func(c map[string]any) { c["aud"] = "sts.amazonaws.com" }), want: 401},
		{name: "expired", claims: claims(func(c map[string]any) { c["exp"] = 1 }), want: 401},
		{name: "push event", claims: claims(func(c map[string]any) { c["ref"] = "refs/heads/main"; c["event_name"] = "push" }), want: 422},
		{name: "not installed", claims: claims(func(c map[string]any) { c["repository"] = "o/uninstalled"; c["repository_id"] = "44" }), want: 403},
		{name: "repository ID mismatch", claims: claims(func(c map[string]any) { c["repository_id"] = "99" }), want: 403},
		{name: "unknown commit", claims: claims(func(c map[string]any) { c["sha"] = "nope" }), want: 422},
		{name: "not a merge commit", setup: func(h *harness) { h.gh.Repos["o/r"].Commits[mergeSHA] = []string{baseSHA} }, want: 422},
		{name: "unknown PR", claims: claims(func(c map[string]any) { c["ref"] = "refs/pull/8/merge" }), want: 422},
		{name: "invalid plan", body: []byte(`{"not":"a plan"}`), want: 422},
		{name: "too large", setup: func(h *harness) { h.server.MaxBodyBytes = 100 }, want: 413},
		{name: "invalid config", setup: func(h *harness) { h.gh.Repos["o/r"].Files[headSHA+":.tf-plan-cost.yml"] = "typo: 1\n" }, want: 422},
		{name: "bad comment_id", query: "?comment_id=a--b", want: 400},
		{name: "config outside repo", query: "?config=../x.yml", want: 400},
		{name: "absolute config", query: "?config=/etc/x.yml", want: 400},
		{name: "bad region", query: "?region=US-EAST-1", want: 400},
		{name: "bad bool", query: "?comment=maybe", want: 400},
		{name: "pricing error", setup: func(h *harness) { h.server.Prices = pricingtest.New() }, want: 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, recordedPrices(t))
			if tt.setup != nil {
				tt.setup(h)
			}
			var c map[string]any
			if tt.claims != nil {
				c = tt.claims(h)
			}
			body := tt.body
			if body == nil {
				body = plan
			}
			code, resp := h.upload(tt.query, c, body)
			if code != tt.want {
				t.Errorf("status = %d, want %d: %s", code, tt.want, resp)
			}
			if !strings.Contains(resp, `"error"`) {
				t.Errorf("response has no error message: %s", resp)
			}
			if tt.want != 502 && len(h.gh.Checks())+len(h.gh.AllComments()) != 0 {
				t.Errorf("a failed upload wrote to GitHub")
			}
		})
	}

	t.Run("no token", func(t *testing.T) {
		h := newHarness(t, recordedPrices(t))
		if code, _ := h.do(http.MethodPost, "/v1/plans", nil, plan); code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
	})
}

func TestHealthz(t *testing.T) {
	h := newHarness(t, nil)
	if code, body := h.do(http.MethodGet, "/healthz", nil, nil); code != http.StatusOK || body != "ok\n" {
		t.Errorf("healthz = %d %q", code, body)
	}
}
