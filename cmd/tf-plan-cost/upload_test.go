package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenEndpoint fakes ACTIONS_ID_TOKEN_REQUEST_URL: it returns a token from
// the App's issuer with the requested audience.
func tokenEndpoint(t *testing.T, a *testApp, mutate func(map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer request-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims := a.iss.Claims()
		claims["aud"] = r.URL.Query().Get("audience")
		if mutate != nil {
			mutate(claims)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": a.iss.Sign(t, claims)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

type uploadRun struct {
	code           int
	stdout, stderr string
	outputs        string
	summary        string
}

func runUploadTest(t *testing.T, env map[string]string, args ...string) uploadRun {
	t.Helper()
	dir := t.TempDir()
	out, sum := filepath.Join(dir, "output"), filepath.Join(dir, "summary")
	env["GITHUB_OUTPUT"], env["GITHUB_STEP_SUMMARY"] = out, sum
	var stdout, stderr bytes.Buffer
	code := runUpload(context.Background(), args, envFunc(env), &stdout, &stderr, http.DefaultClient)
	o, _ := os.ReadFile(out)
	s, _ := os.ReadFile(sum)
	return uploadRun{code, stdout.String(), stderr.String(), string(o), string(s)}
}

func TestUpload(t *testing.T) {
	a := startApp(t)
	tok := tokenEndpoint(t, a, nil)
	env := map[string]string{"ACTIONS_ID_TOKEN_REQUEST_URL": tok.URL + "/token?api-version=2.0", "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "request-token"}

	r := runUploadTest(t, env, "--app-url", a.base, filepath.Join(testdata, "plans", "update.json"))
	// Over the limit, but the check run is the gate: the step succeeds.
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{"delta-monthly-usd=242.24\n", "exceeded=true\n", "check-url=https://github.com/o/r/runs/", "comment-url=https://github.com/o/r/pull/7"} {
		if !strings.Contains(r.outputs, want) {
			t.Errorf("outputs missing %q:\n%s", want, r.outputs)
		}
	}
	if !strings.HasPrefix(r.summary, "<!-- tf-plan-cost -->") || !strings.Contains(r.stdout, "::warning") {
		t.Errorf("summary %q, stdout %q", r.summary, r.stdout)
	}
	if n := len(a.gh.Checks()); n != 1 {
		t.Errorf("check runs = %d", n)
	}
}

func TestUploadOptions(t *testing.T) {
	a := startApp(t)
	tok := tokenEndpoint(t, a, nil)
	env := map[string]string{"ACTIONS_ID_TOKEN_REQUEST_URL": tok.URL, "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "request-token"}
	r := runUploadTest(t, env, "--app-url", a.base+"/", "--comment-id", "prod", "--comment=false", "--region", "us-east-1",
		filepath.Join(testdata, "plans", "update.json"))
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if c := a.gh.Checks(); len(c) != 1 || c[0].Name != "tf-plan-cost / prod" {
		t.Errorf("check runs = %+v", c)
	}
	if n := len(a.gh.AllComments()); n != 0 {
		t.Errorf("--comment=false commented")
	}
}

func TestUploadErrors(t *testing.T) {
	a := startApp(t)
	plan := filepath.Join(testdata, "plans", "update.json")
	okEnv := func(tok *httptest.Server) map[string]string {
		return map[string]string{"ACTIONS_ID_TOKEN_REQUEST_URL": tok.URL, "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "request-token"}
	}

	t.Run("no id-token permission", func(t *testing.T) {
		r := runUploadTest(t, map[string]string{}, "--app-url", a.base, plan)
		if r.code != 1 || !strings.Contains(r.stderr, "id-token: write") {
			t.Errorf("exit %d: %s", r.code, r.stderr)
		}
	})
	t.Run("App not installed", func(t *testing.T) {
		tok := tokenEndpoint(t, a, func(c map[string]any) { c["repository"] = "o/other"; c["repository_id"] = "50" })
		r := runUploadTest(t, okEnv(tok), "--app-url", a.base, plan)
		if r.code != 1 || !strings.Contains(r.stderr, "HTTP 403") || !strings.Contains(r.stderr, "not installed on o/other") {
			t.Errorf("exit %d: %s", r.code, r.stderr)
		}
	})
	t.Run("wrong audience", func(t *testing.T) {
		tok := tokenEndpoint(t, a, nil)
		r := runUploadTest(t, okEnv(tok), "--app-url", a.base, "--audience", "other", plan)
		if r.code != 1 || !strings.Contains(r.stderr, "HTTP 401") {
			t.Errorf("exit %d: %s", r.code, r.stderr)
		}
	})
	t.Run("missing plan", func(t *testing.T) {
		tok := tokenEndpoint(t, a, nil)
		if r := runUploadTest(t, okEnv(tok), "--app-url", a.base, "missing.json"); r.code != 1 {
			t.Errorf("exit %d", r.code)
		}
	})
	t.Run("usage", func(t *testing.T) {
		for _, args := range [][]string{
			{plan},
			{"--app-url", "http://example.com", plan},
			{"--app-url", a.base, "--comment-id", "a--b", plan},
		} {
			if r := runUploadTest(t, map[string]string{}, args...); r.code != 2 {
				t.Errorf("%v: exit %d, want 2", args, r.code)
			}
		}
	})
	if n := len(a.gh.Checks()) + len(a.gh.AllComments()); n != 0 {
		t.Errorf("failed uploads wrote to GitHub (%d)", n)
	}
}
