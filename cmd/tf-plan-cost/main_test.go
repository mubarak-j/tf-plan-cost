package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

const testdata = "../../testdata"

// fixtures are the plans under testdata/plans.
var fixtures = []string{"create", "update", "delete", "replace", "mixed", "noop"}

func factory(pc pricing.PriceClient) clientFactory {
	return func(context.Context, string, bool) (pricing.PriceClient, error) { return pc, nil }
}

// TestDiffGolden runs the CLI on every fixture plan with recorded AWS prices
// (testdata/prices.json) and compares the output with testdata/golden.
// Regenerate with: go test ./cmd/tf-plan-cost -update
func TestDiffGolden(t *testing.T) {
	prices, err := pricingtest.Load(filepath.Join(testdata, "prices.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fixtures {
		for _, format := range []string{"markdown", "json"} {
			ext := map[string]string{"markdown": ".md", "json": ".json"}[format]
			t.Run(name+ext, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				planPath := filepath.Join(testdata, "plans", name+".json")
				code := run(context.Background(), []string{"diff", "--region=", "--format", format, planPath},
					nil, &stdout, &stderr, factory(prices))
				if code != 0 {
					t.Fatalf("exit code %d, stderr:\n%s", code, stderr.String())
				}
				golden := filepath.Join(testdata, "golden", name+ext)
				if *update {
					if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, stdout.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run with -update to create)", err)
				}
				if got := stdout.String(); got != string(want) {
					t.Errorf("output differs from %s (run with -update after checking):\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
				}
			})
		}
	}
}

func TestRunUsage(t *testing.T) {
	prices := pricingtest.New()
	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 2, wantStderr: `unknown command "bogus"`},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "tf-plan-cost dev"},
		{name: "help", args: []string{"--help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "diff without plan", args: []string{"diff"}, wantCode: 2, wantStderr: "Usage: tf-plan-cost diff"},
		{name: "diff two plans", args: []string{"diff", "a.json", "b.json"}, wantCode: 2, wantStderr: "Usage: tf-plan-cost diff"},
		{name: "diff bad format", args: []string{"diff", "--format", "xml", "a.json"}, wantCode: 2, wantStderr: `invalid --format "xml"`},
		{name: "diff missing file", args: []string{"diff", "does-not-exist.json"}, wantCode: 1, wantStderr: "does-not-exist.json"},
		{name: "diff not a plan", args: []string{"diff", "-"}, stdin: `{"foo": 1}`, wantCode: 1, wantStderr: "missing format_version"},
		{name: "diff empty plan from stdin, flag after arg", args: []string{"diff", "-", "--format", "json"},
			stdin: `{"format_version": "1.2"}`, wantCode: 0, wantStdout: `"delta_monthly_usd": "0"`},
		{name: "diff help", args: []string{"diff", "-h"}, wantCode: 0, wantStderr: "-region"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, strings.NewReader(tt.stdin), &stdout, &stderr, factory(prices))
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunPricingErrorShowsHint(t *testing.T) {
	// An empty fake fails every lookup with a non-"no price" error, like a
	// missing-credentials failure would.
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"diff", filepath.Join(testdata, "plans", "delete.json")},
		nil, &stdout, &stderr, factory(pricingtest.New()))
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "pricing:GetProducts") {
		t.Errorf("stderr = %q, want credentials hint", stderr.String())
	}
}
