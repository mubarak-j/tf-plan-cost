package pricing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	if s == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAttrs(t *testing.T) {
	a := NewAttrs(
		decode(t, `{"instance_type": "m6i.large", "size": 500, "multi_az": true, "types": ["a", "b"],
			"root_block_device": [{"volume_size": 30, "volume_type": "gp3"}], "tenancy": null}`),
		decode(t, `{"root_block_device": [{"iops": true}], "ebs_block_device": true, "capacity_type": true}`),
	)

	if s, err := a.String("instance_type"); err != nil || s != "m6i.large" {
		t.Errorf("String(instance_type) = %q, %v", s, err)
	}
	if s, err := a.String("tenancy"); err != nil || s != "" {
		t.Errorf("String(null) = %q, %v", s, err)
	}
	if n, ok, err := a.Number("size"); err != nil || !ok || !n.Equal(decimal.NewFromInt(500)) {
		t.Errorf("Number(size) = %s, %v, %v", n, ok, err)
	}
	if _, ok, err := a.Number("missing"); err != nil || ok {
		t.Errorf("Number(missing) ok=%v err=%v, want false, nil", ok, err)
	}
	if n, _, err := a.Number("root_block_device", 0, "volume_size"); err != nil || n.String() != "30" {
		t.Errorf("nested Number = %s, %v", n, err)
	}
	if b, err := a.Bool("multi_az"); err != nil || !b {
		t.Errorf("Bool = %v, %v", b, err)
	}
	if l, err := a.Strings("types"); err != nil || strings.Join(l, ",") != "a,b" {
		t.Errorf("Strings = %v, %v", l, err)
	}

	// Unknown values are errors that read as unpriced reasons.
	_, _, err := a.Number("root_block_device", 0, "iops")
	if reason, ok := UnpricedReason(err); !ok || reason != "root_block_device[0].iops is known only after apply" {
		t.Errorf("unknown nested: reason=%q ok=%v", reason, ok)
	}
	if _, err := a.Len("ebs_block_device"); err == nil {
		t.Error("Len(unknown list) should fail")
	}
	// A whole unknown subtree makes everything under it unknown.
	if _, unknown := a.Get("ebs_block_device", 0, "device_name"); !unknown {
		t.Error("descendant of unknown should be unknown")
	}

	// Type mismatches are real errors, not unpriced.
	_, err = a.String("size")
	if _, ok := UnpricedReason(err); ok || err == nil {
		t.Errorf("type mismatch err = %v, want non-unpriced error", err)
	}
}

func TestAttrsDefaults(t *testing.T) {
	a := NewAttrs(
		decode(t, `{"capacity_type": "SPOT", "iops": 6000, "multi_az": null}`),
		decode(t, `{"tenancy": true, "throughput": true, "encrypted": true}`),
	)
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"set string", must(a.StringOr("ON_DEMAND", "capacity_type")), "SPOT"},
		{"unknown string", must(a.StringOr("default", "tenancy")), "default"},
		{"absent string", must(a.StringOr("zonal", "availability_mode")), "zonal"},
		{"set number", must(a.NumberOr(decimal.Zero, "iops")).String(), "6000"},
		{"unknown number", must(a.NumberOr(decimal.NewFromInt(125), "throughput")).String(), "125"},
		{"null bool", must(a.BoolOr(false, "multi_az")), false},
		{"unknown bool", must(a.BoolOr(true, "encrypted")), true},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestNewEstimateSumsComponents(t *testing.T) {
	e := NewEstimate("x", []Component{
		{Name: "a", Monthly: decimal.RequireFromString("70.08")},
		{Name: "b", Monthly: decimal.RequireFromString("2.40")},
	}, "note")
	if e.Monthly.String() != "72.48" {
		t.Errorf("Monthly = %s, want 72.48", e.Monthly)
	}
	if len(e.Notes) != 1 {
		t.Errorf("Notes = %v", e.Notes)
	}
}
