package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantLimit string // "" = no limit
		wantErr   string
	}{
		{name: "empty file", yaml: ""},
		{name: "comments only", yaml: "# nothing yet\n"},
		{name: "integer", yaml: "fail_above_monthly_usd: 500\n", wantLimit: "500"},
		{name: "decimal", yaml: "fail_above_monthly_usd: 99.95\n", wantLimit: "99.95"},
		{name: "quoted", yaml: "fail_above_monthly_usd: \"250\"\n", wantLimit: "250"},
		{name: "zero fails any increase", yaml: "fail_above_monthly_usd: 0\n", wantLimit: "0"},
		{name: "not a number", yaml: "fail_above_monthly_usd: lots\n", wantErr: `"lots" is not a number`},
		{name: "negative", yaml: "fail_above_monthly_usd: -5\n", wantErr: "must not be negative"},
		{name: "typo in key", yaml: "fail_above_monthly: 500\n", wantErr: "field fail_above_monthly not found"},
		{name: "invalid yaml", yaml: "fail_above_monthly_usd: [\n", wantErr: "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.wantLimit == "" && cfg.FailAboveMonthlyUSD != nil:
				t.Errorf("limit = %s, want none", cfg.FailAboveMonthlyUSD)
			case tt.wantLimit != "" && (cfg.FailAboveMonthlyUSD == nil || !cfg.FailAboveMonthlyUSD.Equal(decimal.RequireFromString(tt.wantLimit))):
				t.Errorf("limit = %v, want %s", cfg.FailAboveMonthlyUSD, tt.wantLimit)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(filepath.Join(dir, "missing.yml"))
	if err != nil || cfg.FailAboveMonthlyUSD != nil {
		t.Fatalf("missing file: cfg=%+v err=%v, want empty config", cfg, err)
	}

	path := filepath.Join(dir, DefaultPath)
	if err := os.WriteFile(path, []byte("fail_above_monthly_usd: 500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.FailAboveMonthlyUSD == nil || cfg.FailAboveMonthlyUSD.String() != "500" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}

	if err := os.WriteFile(path, []byte("nope: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name the file", err)
	}
}

func TestExceeded(t *testing.T) {
	limit := decimal.NewFromInt(500)
	with := &Config{FailAboveMonthlyUSD: &limit}
	tests := []struct {
		cfg   *Config
		delta string
		want  bool
	}{
		{with, "500.01", true},
		{with, "500", false}, // "above", not "at"
		{with, "-9000", false},
		{&Config{}, "1000000", false},
	}
	for _, tt := range tests {
		if got := tt.cfg.Exceeded(decimal.RequireFromString(tt.delta)); got != tt.want {
			t.Errorf("Exceeded(%s) with limit %v = %v, want %v", tt.delta, tt.cfg.FailAboveMonthlyUSD, got, tt.want)
		}
	}
}
