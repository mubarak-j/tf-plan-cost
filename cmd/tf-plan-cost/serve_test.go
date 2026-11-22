package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mubarak-j/tf-plan-cost/internal/app/oidctest"
	"github.com/mubarak-j/tf-plan-cost/internal/github/githubtest"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

func testPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadServeConfig(t *testing.T) {
	key := testPEM(t)
	keyFile := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"GITHUB_APP_ID": "123", "GITHUB_PRIVATE_KEY": key, "GITHUB_WEBHOOK_SECRET": "s"}

	cfg, err := loadServeConfig(envFunc(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.appID != 123 || cfg.addr != ":8080" || string(cfg.privateKey) != key {
		t.Errorf("cfg = %+v", cfg)
	}

	withFile := map[string]string{"GITHUB_APP_ID": "123", "GITHUB_PRIVATE_KEY": keyFile, "GITHUB_WEBHOOK_SECRET": "s", "PORT": "9000"}
	cfg, err = loadServeConfig(envFunc(withFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.privateKey) != key || cfg.addr != ":9000" {
		t.Errorf("key from file: cfg = %+v", cfg)
	}

	for name, env := range map[string]map[string]string{
		"missing":      {},
		"bad app id":   {"GITHUB_APP_ID": "abc", "GITHUB_PRIVATE_KEY": key, "GITHUB_WEBHOOK_SECRET": "s"},
		"missing file": {"GITHUB_APP_ID": "1", "GITHUB_PRIVATE_KEY": "/nonexistent.pem", "GITHUB_WEBHOOK_SECRET": "s"},
	} {
		if _, err := loadServeConfig(envFunc(env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, err = loadServeConfig(envFunc(map[string]string{"GITHUB_APP_ID": "1"}))
	if err == nil || !strings.Contains(err.Error(), "GITHUB_PRIVATE_KEY, GITHUB_WEBHOOK_SECRET") {
		t.Errorf("missing vars error = %v", err)
	}
}

func TestServeMissingConfig(t *testing.T) {
	var stderr bytes.Buffer
	code := runServe(context.Background(), nil, envFunc(nil), &stderr, nil, nil)
	if code != 1 || !strings.Contains(stderr.String(), "GITHUB_APP_ID") {
		t.Errorf("exit %d, stderr %s", code, stderr.String())
	}
}

// TestServeEndToEnd runs the real server against a fake GitHub, a fake OIDC
// issuer and recorded prices: upload a stripped plan, get a check run and a
// PR comment.
func TestServeEndToEnd(t *testing.T) {
	fake := githubtest.NewServer(t)
	fake.AddRepo("o/r", &githubtest.Repo{
		ID: 42, Installation: 7,
		PRs:     map[int]string{7: "head-sha"},
		Commits: map[string][]string{"merge-sha": {"base-sha", "head-sha"}},
		Files:   map[string]string{"head-sha:.tf-plan-cost.yml": "fail_above_monthly_usd: 100\n"},
	})
	iss := oidctest.NewIssuer(t)
	prices, err := pricingtest.Load(filepath.Join(testdata, "prices.json"))
	if err != nil {
		t.Fatal(err)
	}

	env := envFunc(map[string]string{
		"GITHUB_APP_ID": "123", "GITHUB_PRIVATE_KEY": testPEM(t), "GITHUB_WEBHOOK_SECRET": "s",
		"PORT": "0", "TF_PLAN_COST_LISTEN_HOST": "127.0.0.1",
		"GITHUB_API_URL": fake.URL, "TF_PLAN_COST_OIDC_ISSUER": iss.URL,
	})
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan string, 1)
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() { done <- runServe(ctx, nil, env, &stderr, factory(prices), func(a string) { addr <- a }) }()

	var base string
	select {
	case a := <-addr:
		base = "http://" + a
	case code := <-done:
		t.Fatalf("serve exited %d: %s", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}

	// Strip the plan the way the Action does, then upload it.
	var stripped bytes.Buffer
	if code := run(ctx, []string{"strip", filepath.Join(testdata, "plans", "update.json")}, nil, &stripped, &stderr, nil); code != 0 {
		t.Fatalf("strip: %s", stderr.String())
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/plans", &stripped)
	req.Header.Set("Authorization", "Bearer "+iss.Sign(t, iss.Claims()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Delta    string `json:"delta_monthly_usd"`
		Exceeded bool   `json:"exceeded"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	// update.json adds $242.24/mo, above the $100 limit.
	if out.Delta != "242.24" || !out.Exceeded {
		t.Errorf("response = %s", body)
	}
	if checks := fake.Checks(); len(checks) != 1 || checks[0].Conclusion != "failure" || checks[0].Output.Title != "+$242.24/mo" {
		t.Errorf("check runs = %+v", checks)
	}
	if comments := fake.AllComments(); len(comments) != 1 || comments[0].Login != "tf-plan-cost[bot]" {
		t.Errorf("comments = %+v", comments)
	}

	if r, err := http.Get(base + "/healthz"); err != nil || r.StatusCode != http.StatusOK {
		t.Errorf("healthz: %v %v", r, err)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("serve exit %d: %s", code, stderr.String())
		}
	case <-time.After(35 * time.Second):
		t.Fatal("server did not shut down")
	}
	if strings.Contains(stderr.String(), "aws_instance") {
		t.Errorf("server logged plan contents:\n%s", stderr.String())
	}
}
