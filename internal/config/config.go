// Package config loads the per-repository .tf-plan-cost.yml file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// DefaultPath is where the config file is looked up by default.
const DefaultPath = ".tf-plan-cost.yml"

// Config is the repository configuration.
type Config struct {
	// FailAboveMonthlyUSD fails the check when the monthly cost increase is
	// greater than this amount. Nil means no limit. Decreases never fail.
	FailAboveMonthlyUSD *decimal.Decimal
}

type rawConfig struct {
	FailAboveMonthlyUSD *string `yaml:"fail_above_monthly_usd"`
}

// Load reads the config file at path. A missing file is not an error and
// yields an empty Config, so the file stays optional.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes config YAML. Unknown keys are rejected so a typo such as
// `fail_above_monthly` does not silently disable the limit.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	cfg := &Config{}
	if raw.FailAboveMonthlyUSD != nil {
		limit, err := decimal.NewFromString(*raw.FailAboveMonthlyUSD)
		if err != nil {
			return nil, fmt.Errorf("fail_above_monthly_usd: %q is not a number", *raw.FailAboveMonthlyUSD)
		}
		if limit.IsNegative() {
			return nil, fmt.Errorf("fail_above_monthly_usd: must not be negative, got %s", limit)
		}
		cfg.FailAboveMonthlyUSD = &limit
	}
	return cfg, nil
}

// Exceeded reports whether a monthly cost delta is above the limit.
func (c *Config) Exceeded(delta decimal.Decimal) bool {
	return c.FailAboveMonthlyUSD != nil && delta.GreaterThan(*c.FailAboveMonthlyUSD)
}
