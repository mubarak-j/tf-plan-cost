package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mubarak-j/tf-plan-cost/internal/github"
	"github.com/mubarak-j/tf-plan-cost/internal/github/githubtest"
	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

const (
	testAppID  = 123
	testSecret = "webhook-secret"
	headSHA    = "head-sha"
	baseSHA    = "base-sha"
	mergeSHA   = "merge-sha"
)

// harness wires a Server to a fake GitHub, a fake OIDC issuer and fake
// prices. Repository o/r (ID 42) has the App installed and an open PR 7
// whose head is head-sha; the token's merge-sha has parents base-sha and
// head-sha.
type harness struct {
	t      *testing.T
	gh     *githubtest.Server
	iss    *testIssuer
	server *Server
	http   *httptest.Server
	logs   *bytes.Buffer
}

func newHarness(t *testing.T, prices pricing.PriceClient) *harness {
	t.Helper()
	fake := githubtest.NewServer(t)
	fake.AddRepo("o/r", &githubtest.Repo{
		ID: 42, Installation: 7,
		PRs:     map[int]string{7: headSHA},
		Commits: map[string][]string{mergeSHA: {baseSHA, headSHA}},
	})
	fake.AddRepo("o/uninstalled", &githubtest.Repo{ID: 44})

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	ghApp, err := github.NewApp(testAppID, pemKey, fake.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	iss := newTestIssuer(t)
	logs := &bytes.Buffer{}
	s := &Server{
		AppID:         testAppID,
		BotLogin:      "tf-plan-cost[bot]",
		WebhookSecret: []byte(testSecret),
		GitHub:        ghApp,
		Verifier:      iss.Verifier(t.Context(), time.Now),
		Prices:        prices,
		Logger:        slog.New(slog.NewTextHandler(logs, nil)),
	}
	h := &harness{t: t, gh: fake, iss: iss, server: s, logs: logs}
	h.http = httptest.NewServer(s.Handler())
	t.Cleanup(h.http.Close)
	return h
}

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// do sends a request and returns the status and body.
func (h *harness) do(method, path string, header map[string]string, body []byte) (int, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.http.URL+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (h *harness) webhook(event string, body []byte) (int, string) {
	h.t.Helper()
	return h.do(http.MethodPost, "/webhook", map[string]string{
		"X-GitHub-Event": event, "X-GitHub-Delivery": "d1", "X-Hub-Signature-256": sign(body),
		"Content-Type": "application/json",
	}, body)
}

func stripFixture(t *testing.T, path string, w io.Writer) error {
	t.Helper()
	f, err := os.Open(filepath.Join(testdata, path))
	if err != nil {
		return err
	}
	defer f.Close()
	return plan.Strip(f, w)
}
