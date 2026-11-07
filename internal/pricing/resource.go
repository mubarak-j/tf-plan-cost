package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Resource is one side (before or after) of a resource change, ready to price.
type Resource struct {
	Type    string
	Address string
	Region  string
	Attrs   Attrs
}

// Component is one priced part of a resource, e.g. compute or root volume.
type Component struct {
	Name    string          `json:"name"`
	Monthly decimal.Decimal `json:"monthly_usd"`
	Detail  string          `json:"detail"`
}

// Estimate is the monthly cost of one side of a resource change.
type Estimate struct {
	Monthly    decimal.Decimal `json:"monthly_usd"`
	Summary    string          `json:"summary"`    // short description, e.g. "m6i.large"
	Components []Component     `json:"components"` // priced parts
	Notes      []string        `json:"notes,omitempty"`
}

// NewEstimate sums the components into an Estimate.
func NewEstimate(summary string, components []Component, notes ...string) *Estimate {
	e := &Estimate{Summary: summary, Components: components, Notes: notes}
	for _, c := range components {
		e.Monthly = e.Monthly.Add(c.Monthly)
	}
	return e
}

// ResourcePricer prices one Terraform resource type.
type ResourcePricer interface {
	Price(ctx context.Context, r Resource, pc PriceClient) (*Estimate, error)
}

// PricerFunc adapts a function to ResourcePricer.
type PricerFunc func(ctx context.Context, r Resource, pc PriceClient) (*Estimate, error)

// Price implements ResourcePricer.
func (f PricerFunc) Price(ctx context.Context, r Resource, pc PriceClient) (*Estimate, error) {
	return f(ctx, r, pc)
}

// UnpricedError means the resource cannot be priced without guessing.
type UnpricedError struct{ Reason string }

func (e *UnpricedError) Error() string { return e.Reason }

// Unpriced returns an UnpricedError with a formatted reason.
func Unpriced(format string, args ...any) error {
	return &UnpricedError{Reason: fmt.Sprintf(format, args...)}
}

// UnpricedReason reports whether err means "cannot be priced" (including no
// or ambiguous Price List matches) and, if so, why.
func UnpricedReason(err error) (string, bool) {
	var ue *UnpricedError
	if errors.As(err, &ue) {
		return ue.Reason, true
	}
	if errors.Is(err, ErrNoPrice) || errors.Is(err, ErrAmbiguousPrice) {
		return err.Error(), true
	}
	return "", false
}

// Attrs gives typed access to a resource's attribute values, treating values
// that are "known after apply" as errors so pricers never guess.
type Attrs struct {
	values  map[string]any
	unknown any // mirrors values; true leaf = unknown
}

// NewAttrs wraps attribute values and the matching after_unknown tree (nil
// when every value is known, as for `before`).
func NewAttrs(values map[string]any, unknown map[string]any) Attrs {
	a := Attrs{values: values}
	if unknown != nil {
		a.unknown = unknown
	}
	return a
}

// Get walks a path of map keys (string) and list indexes (int). It returns
// the value (nil if absent) and whether it is unknown until apply.
func (a Attrs) Get(path ...any) (value any, unknown bool) {
	var v any = a.values
	u := a.unknown
	for _, step := range path {
		if b, ok := u.(bool); ok && b {
			return nil, true
		}
		v, u = descend(v, step), descend(u, step)
	}
	if b, ok := u.(bool); ok && b {
		return nil, true
	}
	return v, false
}

func descend(v any, step any) any {
	switch s := step.(type) {
	case string:
		if m, ok := v.(map[string]any); ok {
			return m[s]
		}
	case int:
		if l, ok := v.([]any); ok && s >= 0 && s < len(l) {
			return l[s]
		}
	}
	return nil
}

func pathString(path []any) string {
	var b strings.Builder
	for i, p := range path {
		switch s := p.(type) {
		case int:
			fmt.Fprintf(&b, "[%d]", s)
		default:
			if i > 0 {
				b.WriteByte('.')
			}
			fmt.Fprint(&b, s)
		}
	}
	return b.String()
}

func unknownErr(path []any) error {
	return Unpriced("%s is known only after apply", pathString(path))
}

// String returns a string attribute ("" if absent or null).
func (a Attrs) String(path ...any) (string, error) {
	v, unknown := a.Get(path...)
	if unknown {
		return "", unknownErr(path)
	}
	switch s := v.(type) {
	case nil:
		return "", nil
	case string:
		return s, nil
	default:
		return "", fmt.Errorf("%s: expected string, got %T", pathString(path), v)
	}
}

// Number returns a numeric attribute; ok is false if absent or null.
func (a Attrs) Number(path ...any) (n decimal.Decimal, ok bool, err error) {
	v, unknown := a.Get(path...)
	if unknown {
		return decimal.Zero, false, unknownErr(path)
	}
	switch x := v.(type) {
	case nil:
		return decimal.Zero, false, nil
	case json.Number:
		d, err := decimal.NewFromString(x.String())
		return d, err == nil, err
	case float64:
		return decimal.NewFromFloat(x), true, nil
	case int:
		return decimal.NewFromInt(int64(x)), true, nil
	default:
		return decimal.Zero, false, fmt.Errorf("%s: expected number, got %T", pathString(path), v)
	}
}

// Bool returns a boolean attribute (false if absent or null).
func (a Attrs) Bool(path ...any) (bool, error) {
	v, unknown := a.Get(path...)
	if unknown {
		return false, unknownErr(path)
	}
	switch b := v.(type) {
	case nil:
		return false, nil
	case bool:
		return b, nil
	default:
		return false, fmt.Errorf("%s: expected bool, got %T", pathString(path), v)
	}
}

// The ...Or accessors are for optional attributes that only qualify a price
// (spot vs on-demand, tenancy, baseline IOPS). When such an attribute is not
// set, Terraform shows it as "known after apply" because the provider fills in
// the AWS default, so an absent or unknown value means def. Never use them for
// attributes that determine the price itself, such as instance type or size.

// StringOr returns a string attribute, or def if it is absent or unknown.
func (a Attrs) StringOr(def string, path ...any) (string, error) {
	if _, unknown := a.Get(path...); unknown {
		return def, nil
	}
	s, err := a.String(path...)
	if err != nil || s == "" {
		return def, err
	}
	return s, nil
}

// NumberOr returns a numeric attribute, or def if it is absent or unknown.
func (a Attrs) NumberOr(def decimal.Decimal, path ...any) (decimal.Decimal, error) {
	if _, unknown := a.Get(path...); unknown {
		return def, nil
	}
	n, ok, err := a.Number(path...)
	if err != nil || !ok {
		return def, err
	}
	return n, nil
}

// BoolOr returns a boolean attribute, or def if it is absent or unknown.
func (a Attrs) BoolOr(def bool, path ...any) (bool, error) {
	v, unknown := a.Get(path...)
	if unknown || v == nil {
		return def, nil
	}
	return a.Bool(path...)
}

// Len returns the length of a list attribute (0 if absent or null).
func (a Attrs) Len(path ...any) (int, error) {
	v, unknown := a.Get(path...)
	if unknown {
		return 0, unknownErr(path)
	}
	l, _ := v.([]any)
	return len(l), nil
}

// Strings returns a list-of-strings attribute.
func (a Attrs) Strings(path ...any) ([]string, error) {
	n, err := a.Len(path...)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := range n {
		s, err := a.String(append(append([]any{}, path...), i)...)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
