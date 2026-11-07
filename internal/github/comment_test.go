package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeComment struct {
	ID    int64
	Body  string
	Login string
}

// fakeGitHub implements the few REST endpoints Commenter uses. It pages
// comment listings two at a time to exercise pagination.
type fakeGitHub struct {
	t         *testing.T
	mu        sync.Mutex
	comments  []fakeComment
	nextID    int64
	forbidden bool   // reject writes with 403, like a fork PR's token
	login     string // "" = GET /user returns 403 (installation token)
	writes    int
}

const pageSize = 2

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		f.t.Errorf("Authorization = %q", got)
	}
	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	forbidden := func() {
		writeJSON(http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
	}
	toJSON := func(c fakeComment) map[string]any {
		return map[string]any{
			"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login},
			"html_url": fmt.Sprintf("https://github.com/o/r/pull/7#issuecomment-%d", c.ID),
		}
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		if f.login == "" {
			forbidden()
			return
		}
		writeJSON(http.StatusOK, map[string]string{"login": f.login})

	case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7/comments":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * pageSize
		end := min(start+pageSize, len(f.comments))
		var out []map[string]any
		for _, c := range f.comments[min(start, len(f.comments)):end] {
			out = append(out, toJSON(c))
		}
		if end < len(f.comments) {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=%d>; rel="next"`, r.Host, r.URL.Path, page+1))
		}
		writeJSON(http.StatusOK, out)

	case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/7/comments":
		if f.forbidden {
			forbidden()
			return
		}
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.nextID++
		c := fakeComment{ID: f.nextID, Body: req.Body, Login: "github-actions[bot]"}
		f.comments = append(f.comments, c)
		f.writes++
		writeJSON(http.StatusCreated, toJSON(c))

	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/o/r/issues/comments/"):
		if f.forbidden {
			forbidden()
			return
		}
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/repos/o/r/issues/comments/"), 10, 64)
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments[i].Body = req.Body
				f.writes++
				writeJSON(http.StatusOK, toJSON(f.comments[i]))
				return
			}
		}
		writeJSON(http.StatusNotFound, map[string]string{"message": "Not Found"})

	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func newFake(t *testing.T, comments ...fakeComment) (*fakeGitHub, *Commenter) {
	t.Helper()
	f := &fakeGitHub{t: t, comments: comments, nextID: 100}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client, err := NewClient("test-token", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return f, &Commenter{Client: client, Owner: "o", Repo: "r", Author: "github-actions[bot]"}
}

const marker = "<!-- tf-plan-cost -->"

func TestUpsert(t *testing.T) {
	ctx := context.Background()
	body := marker + "\n### tf-plan-cost: **+$1.00/mo**"
	human := fakeComment{ID: 1, Body: "LGTM", Login: "alice"}
	quoted := fakeComment{ID: 2, Body: marker + "\nquoted by a person", Login: "alice"}
	otherRoot := fakeComment{ID: 3, Body: "<!-- tf-plan-cost:prod -->\nprod", Login: "github-actions[bot]"}
	ours := fakeComment{ID: 4, Body: marker + "\nold", Login: "github-actions[bot]"}

	tests := []struct {
		name        string
		existing    []fakeComment
		body        string
		create      bool
		wantOutcome Outcome
		wantID      int64 // comment that should hold body afterwards (0 = none)
		wantWrites  int
	}{
		{name: "creates when missing", existing: []fakeComment{human}, body: body, create: true, wantOutcome: Created, wantID: 101, wantWrites: 1},
		{name: "skips when missing and create=false", existing: []fakeComment{human}, body: body, create: false, wantOutcome: Skipped},
		// ours is on page 2 (page size 2), behind a person quoting the marker
		// and another root's comment.
		{name: "updates ours across pages, ignoring others", existing: []fakeComment{human, quoted, otherRoot, ours}, body: body, create: true, wantOutcome: Updated, wantID: 4, wantWrites: 1},
		{name: "updates even when create=false", existing: []fakeComment{ours}, body: body, create: false, wantOutcome: Updated, wantID: 4, wantWrites: 1},
		{name: "unchanged body is not rewritten", existing: []fakeComment{{ID: 4, Body: body, Login: "github-actions[bot]"}}, body: body, create: true, wantOutcome: Unchanged, wantID: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := newFake(t, tt.existing...)
			res, err := c.Upsert(ctx, 7, marker, tt.body, tt.create)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %s, want %s", res.Outcome, tt.wantOutcome)
			}
			if f.writes != tt.wantWrites {
				t.Errorf("writes = %d, want %d", f.writes, tt.wantWrites)
			}
			if tt.wantID == 0 {
				if res.URL != "" {
					t.Errorf("URL = %q, want none", res.URL)
				}
				return
			}
			if want := fmt.Sprintf("#issuecomment-%d", tt.wantID); !strings.HasSuffix(res.URL, want) {
				t.Errorf("URL = %q, want suffix %q", res.URL, want)
			}
			for _, cm := range f.comments {
				if cm.ID == tt.wantID && cm.Body != tt.body {
					t.Errorf("comment %d body = %q", cm.ID, cm.Body)
				}
				if cm.ID != tt.wantID && cm.Body == tt.body {
					t.Errorf("comment %d was overwritten", cm.ID)
				}
			}
		})
	}
}

func TestUpsertNoAuthorFilter(t *testing.T) {
	// With no Author, any comment starting with the marker matches.
	_, c := newFake(t, fakeComment{ID: 9, Body: marker + "\nold", Login: "my-pat-user"})
	c.Author = ""
	res, err := c.Upsert(context.Background(), 7, marker, marker+"\nnew", true)
	if err != nil || res.Outcome != Updated {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestUpsertErrors(t *testing.T) {
	f, c := newFake(t)
	f.forbidden = true
	_, err := c.Upsert(context.Background(), 7, marker, marker+"\nx", true)
	if !IsForbidden(err) {
		t.Errorf("err = %v, want 403", err)
	}

	if _, err := c.Upsert(context.Background(), 7, marker, "no marker", true); err == nil {
		t.Error("body without marker should be rejected")
	}
	if IsForbidden(fmt.Errorf("plain")) {
		t.Error("IsForbidden(plain error) = true")
	}
}

func TestTokenLogin(t *testing.T) {
	f, c := newFake(t)
	if _, err := TokenLogin(context.Background(), c.Client); err == nil {
		t.Error("installation token: want error")
	}
	f.login = "octocat"
	if login, err := TokenLogin(context.Background(), c.Client); err != nil || login != "octocat" {
		t.Errorf("login=%q err=%v", login, err)
	}
}

func TestNewClientEnterpriseURL(t *testing.T) {
	c, err := NewClient("t", "https://ghe.example.com/api/v3")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.BaseURL(); got != "https://ghe.example.com/api/v3/" {
		t.Errorf("BaseURL = %s", got)
	}
	c, err = NewClient("t", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.BaseURL(); got != "https://api.github.com/" {
		t.Errorf("BaseURL = %s", got)
	}
}

func TestActionsEnv(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name    string
		payload string
		want    *PullRequest
	}{
		{"same-repo PR", `{"pull_request": {"number": 7, "head": {"repo": {"full_name": "o/r"}}, "base": {"repo": {"full_name": "o/r"}}}}`, &PullRequest{Number: 7}},
		{"fork PR", `{"pull_request": {"number": 8, "head": {"repo": {"full_name": "fork/r"}}, "base": {"repo": {"full_name": "o/r"}}}}`, &PullRequest{Number: 8, Fork: true}},
		{"deleted fork", `{"pull_request": {"number": 9, "head": {"repo": null}, "base": {"repo": {"full_name": "o/r"}}}}`, &PullRequest{Number: 9, Fork: true}},
		{"push event", `{"ref": "refs/heads/main"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ActionsEnv{EventPath: write("event.json", tt.payload)}
			got, err := env.PullRequest()
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("PullRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}

	if pr, err := (ActionsEnv{}).PullRequest(); pr != nil || err != nil {
		t.Errorf("no event path: %v %v", pr, err)
	}

	env := ActionsEnv{Repository: "o/r", Output: filepath.Join(dir, "out"), StepSummary: filepath.Join(dir, "summary")}
	if o, r, err := env.OwnerRepo(); o != "o" || r != "r" || err != nil {
		t.Errorf("OwnerRepo = %q %q %v", o, r, err)
	}
	if _, _, err := (ActionsEnv{Repository: "nope"}).OwnerRepo(); err == nil {
		t.Error("bad GITHUB_REPOSITORY should fail")
	}
	if err := env.SetOutputs([][2]string{{"a", "1"}, {"b", "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := env.SetOutputs([][2]string{{"bad", "two\nlines"}}); err == nil {
		t.Error("multi-line output should fail")
	}
	if err := env.AppendStepSummary("# hi"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(env.Output)
	summary, _ := os.ReadFile(env.StepSummary)
	if string(out) != "a=1\nb=x\n" || string(summary) != "# hi\n" {
		t.Errorf("output=%q summary=%q", out, summary)
	}
	// Unset files are a no-op outside Actions.
	if err := (ActionsEnv{}).SetOutputs([][2]string{{"a", "1"}}); err != nil {
		t.Error(err)
	}
}
