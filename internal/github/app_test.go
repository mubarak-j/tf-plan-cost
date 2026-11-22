package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gh "github.com/google/go-github/v93/github"

	"github.com/mubarak-j/tf-plan-cost/internal/github"
	"github.com/mubarak-j/tf-plan-cost/internal/github/githubtest"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func newApp(t *testing.T) (*githubtest.Server, *github.App) {
	t.Helper()
	srv := githubtest.NewServer(t)
	srv.AddRepo("o/r", &githubtest.Repo{ID: 42, Installation: 7})
	srv.AddRepo("o/other", &githubtest.Repo{ID: 43, Installation: 7})
	srv.AddRepo("o/uninstalled", &githubtest.Repo{ID: 44})
	app, err := github.NewApp(123, testKey(t), srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, app
}

func TestNewAppBadKey(t *testing.T) {
	if _, err := github.NewApp(1, []byte("not a key"), "", nil); err == nil {
		t.Error("NewApp with a bad key succeeded")
	}
}

func TestBotLogin(t *testing.T) {
	_, app := newApp(t)
	login, err := app.BotLogin(context.Background())
	if err != nil || login != "tf-plan-cost[bot]" {
		t.Errorf("BotLogin = %q, %v", login, err)
	}
}

func TestRepoClient(t *testing.T) {
	srv, app := newApp(t)
	ctx := context.Background()

	client, err := app.RepoClient(ctx, "o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Repositories.Get(ctx, "o", "r"); err != nil {
		t.Errorf("client cannot read its repo: %v", err)
	}
	// The token is limited to the one repository.
	if _, _, err := client.Repositories.Get(ctx, "o", "other"); !github.IsNotFound(err) {
		t.Errorf("client reads another repo in the installation: %v", err)
	}
	if !reflect.DeepEqual(srv.TokenRepoIDs, [][]int64{{42}}) {
		t.Errorf("token requests = %v, want [[42]]", srv.TokenRepoIDs)
	}

	// The client is cached: no new token for the same repo.
	if _, err := app.RepoClient(ctx, "o", "r", 42); err != nil {
		t.Fatal(err)
	}
	if len(srv.TokenRepoIDs) != 1 {
		t.Errorf("token requests = %d, want 1 (cached)", len(srv.TokenRepoIDs))
	}
}

func TestRepoClientErrors(t *testing.T) {
	_, app := newApp(t)
	ctx := context.Background()
	if _, err := app.RepoClient(ctx, "o", "uninstalled", 44); !errors.Is(err, github.ErrNotInstalled) {
		t.Errorf("uninstalled repo: err = %v, want ErrNotInstalled", err)
	}
	if _, err := app.RepoClient(ctx, "o", "missing", 1); !errors.Is(err, github.ErrNotInstalled) {
		t.Errorf("missing repo: err = %v, want ErrNotInstalled", err)
	}
	// A token for repository 99 (e.g. o/r was deleted and re-created) is
	// refused: 99 is not in the installation.
	if _, err := app.RepoClient(ctx, "o", "r", 99); !errors.Is(err, github.ErrNotInstalled) {
		t.Errorf("wrong repo ID: err = %v, want ErrNotInstalled", err)
	}
	// Repository 43 is in the installation, but it is not o/r: the token
	// limited to 43 cannot read o/r.
	if _, err := app.RepoClient(ctx, "o", "r", 43); err == nil {
		t.Error("mismatched repo ID: RepoClient succeeded")
	}
}

func TestChecksCreate(t *testing.T) {
	srv, app := newApp(t)
	ctx := context.Background()
	client, err := app.RepoClient(ctx, "o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	checks := &github.Checks{Client: client, Owner: "o", Repo: "r", Now: func() time.Time { return time.Unix(0, 0) }}

	url, err := checks.Create(ctx, github.CheckRun{
		HeadSHA: "abc", Failed: true, Title: "+$600.00/mo", Summary: "report",
		ExternalID: "run-1", DetailsURL: "https://github.com/o/r/actions/runs/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := srv.Checks()
	if len(got) != 1 {
		t.Fatalf("got %d check runs, want 1", len(got))
	}
	cr := got[0]
	if cr.Name != "tf-plan-cost" || cr.HeadSHA != "abc" || cr.Status != "completed" || cr.Conclusion != "failure" ||
		cr.Output.Title != "+$600.00/mo" || cr.ExternalID != "run-1" || !strings.HasSuffix(url, "/runs/"+strconv.FormatInt(cr.ID, 10)) {
		t.Errorf("check run = %+v, url %s", cr, url)
	}

	if _, err := checks.Create(ctx, github.CheckRun{HeadSHA: "def", Title: "No change"}); err != nil {
		t.Fatal(err)
	}
	if c := srv.Checks()[1].Conclusion; c != "success" {
		t.Errorf("conclusion = %q, want success", c)
	}
}

func TestAddRerunHint(t *testing.T) {
	srv, app := newApp(t)
	ctx := context.Background()
	client, _ := app.RepoClient(ctx, "o", "r", 42)
	checks := &github.Checks{Client: client, Owner: "o", Repo: "r"}
	if _, err := checks.Create(ctx, github.CheckRun{HeadSHA: "abc", Title: "+$1.00/mo", Summary: "report",
		DetailsURL: "https://github.com/o/r/actions/runs/1"}); err != nil {
		t.Fatal(err)
	}
	c := srv.Checks()[0]
	cr := &gh.CheckRun{ID: new(c.ID), Name: new(c.Name), DetailsURL: new(c.DetailsURL),
		Output: &gh.CheckRunOutput{Title: new(c.Output.Title), Summary: new(c.Output.Summary)}}

	if err := checks.AddRerunHint(ctx, cr); err != nil {
		t.Fatal(err)
	}
	got := srv.Checks()[0].Output
	if !strings.HasPrefix(got.Summary, "**To re-run") || !strings.Contains(got.Summary, "actions/runs/1") ||
		!strings.HasSuffix(got.Summary, "report") || got.Title != "+$1.00/mo" {
		t.Errorf("output after hint = %+v", got)
	}

	// Adding it again is a no-op.
	cr.Output.Summary = new(got.Summary)
	if err := checks.AddRerunHint(ctx, cr); err != nil {
		t.Fatal(err)
	}
	if again := srv.Checks()[0].Output.Summary; again != got.Summary {
		t.Errorf("hint added twice:\n%s", again)
	}
}

func TestTruncate(t *testing.T) {
	if got := github.Truncate("short", 100); got != "short" {
		t.Errorf("Truncate(short) = %q", got)
	}
	long := strings.Repeat("line of the report\n", 10000)
	got := github.Truncate(long, 65535)
	if len(got) > 65535 || !strings.Contains(got, "Truncated") || !strings.HasPrefix(got, "line of the report\n") {
		t.Errorf("Truncate(long): len %d, tail %q", len(got), got[len(got)-120:])
	}
	// Never splits a multi-byte character, even without line breaks.
	multi := strings.Repeat("€", 30000)
	got = github.Truncate(multi, 65535)
	if len(got) > 65535 || !utf8.ValidString(got) {
		t.Errorf("Truncate(multi-byte) produced invalid UTF-8 or is too long (%d)", len(got))
	}
}
