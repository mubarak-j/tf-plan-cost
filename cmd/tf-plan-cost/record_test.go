//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

// TestRecordPrices queries the live AWS Price List API for every fixture and
// saves the prices to testdata/prices.json, which TestDiffGolden replays.
// It needs AWS credentials allowed to call pricing:GetProducts:
//
//	AWS_PROFILE=... go test -tags integration ./cmd/tf-plan-cost -run TestRecordPrices
//	go test ./cmd/tf-plan-cost -update
func TestRecordPrices(t *testing.T) {
	ctx := context.Background()
	aws, err := pricing.NewAWSClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec := &pricingtest.Recorder{Next: aws}
	for _, name := range fixtures {
		var stderr bytes.Buffer
		code := run(ctx, []string{"diff", "--region=", filepath.Join(testdata, "plans", name+".json")},
			nil, io.Discard, &stderr, factory(rec))
		if code != 0 {
			t.Fatalf("%s: exit code %d: %s", name, code, stderr.String())
		}
	}
	if err := rec.Save(filepath.Join(testdata, "prices.json")); err != nil {
		t.Fatal(err)
	}
}
