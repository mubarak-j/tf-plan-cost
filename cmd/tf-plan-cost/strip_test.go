package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

// TestStripThenDiff checks that a stripped plan gives the same report as the
// original.
func TestStripThenDiff(t *testing.T) {
	prices, err := pricingtest.Load(filepath.Join(testdata, "prices.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			stripped := filepath.Join(t.TempDir(), "stripped.json")
			var stderr bytes.Buffer
			planPath := filepath.Join(testdata, "plans", name+".json")
			if code := run(context.Background(), []string{"strip", planPath, "-o", stripped}, nil, nil, &stderr, nil); code != 0 {
				t.Fatalf("strip: exit %d: %s", code, stderr.String())
			}
			var stdout bytes.Buffer
			if code := run(context.Background(), []string{"diff", "--region=", "--format", "json", stripped},
				nil, &stdout, &stderr, factory(prices)); code != 0 {
				t.Fatalf("diff: exit %d: %s", code, stderr.String())
			}
			want, err := os.ReadFile(filepath.Join(testdata, "golden", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != string(want) {
				t.Errorf("report from stripped plan differs:\n%s", stdout.String())
			}
		})
	}
}

func TestStripStdio(t *testing.T) {
	var stdout, stderr bytes.Buffer
	in := strings.NewReader(`{"format_version":"1.2","resource_changes":[]}`)
	if code := run(context.Background(), []string{"strip", "-"}, in, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"format_version":"1.2"`) {
		t.Errorf("stdout = %s", stdout.String())
	}
}

func TestStripUsage(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"strip"}, nil, nil, &stderr, nil); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if code := run(context.Background(), []string{"strip", "missing.json"}, nil, nil, &stderr, nil); code != 1 {
		t.Errorf("missing file: exit %d, want 1", code)
	}
}
