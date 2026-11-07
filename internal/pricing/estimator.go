package pricing

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
)

// LineItem is the priced change of one resource.
type LineItem struct {
	Address string          `json:"address"`
	Type    string          `json:"type"`
	Action  plan.Action     `json:"action"`
	Before  *Estimate       `json:"before,omitempty"` // nil if the resource did not exist
	After   *Estimate       `json:"after,omitempty"`  // nil if the resource will not exist
	Delta   decimal.Decimal `json:"delta_monthly_usd"`
}

// UnpricedResource is a resource that could not be priced.
type UnpricedResource struct {
	Address string      `json:"address"`
	Type    string      `json:"type"`
	Action  plan.Action `json:"action"`
	Reason  string      `json:"reason"`
}

// Report is the cost estimate of a whole plan.
type Report struct {
	// Items holds every priced resource, including unchanged ones, sorted by
	// absolute delta (largest first) and then address.
	Items []LineItem `json:"items"`
	// Unpriced holds supported resources that could not be priced.
	Unpriced []UnpricedResource `json:"unpriced"`
	// Unsupported holds changed resources whose type has no pricer.
	Unsupported []UnpricedResource `json:"unsupported"`

	TotalBefore decimal.Decimal `json:"total_before_monthly_usd"`
	TotalAfter  decimal.Decimal `json:"total_after_monthly_usd"`
	Delta       decimal.Decimal `json:"delta_monthly_usd"`

	// PricesAsOf is when the oldest price used was fetched from AWS.
	PricesAsOf time.Time `json:"prices_as_of"`
}

// Changed returns the line items whose resource is created, updated, deleted
// or replaced.
func (r *Report) Changed() []LineItem {
	var out []LineItem
	for _, it := range r.Items {
		if it.Action != plan.NoOp {
			out = append(out, it)
		}
	}
	return out
}

// HasChanges reports whether any resource is created, updated, deleted or
// replaced, whether or not it could be priced.
func (r *Report) HasChanges() bool {
	if len(r.Changed()) > 0 || len(r.Unsupported) > 0 {
		return true
	}
	for _, u := range r.Unpriced {
		if u.Action != plan.NoOp {
			return true
		}
	}
	return false
}

// Estimator prices plans.
type Estimator struct {
	Client  PriceClient
	Pricers map[string]ResourcePricer
	// DefaultRegion is used when neither the resource nor its provider
	// configuration names a region.
	DefaultRegion string
}

// Estimate prices every managed resource in the plan. Resources that cannot be
// priced are reported, never guessed. It returns an error only when pricing
// itself fails (e.g. the AWS API is unreachable or credentials are missing).
func (e *Estimator) Estimate(ctx context.Context, p *plan.Plan) (*Report, error) {
	tc := &trackingClient{next: e.Client}
	rep := &Report{Items: []LineItem{}, Unpriced: []UnpricedResource{}, Unsupported: []UnpricedResource{}}

	for _, rc := range p.Changes {
		if rc.Action == plan.Forget {
			continue // leaves Terraform management; its cost does not change
		}
		pricer, ok := e.Pricers[rc.Type]
		if !ok {
			if rc.Action != plan.NoOp {
				rep.Unsupported = append(rep.Unsupported, UnpricedResource{
					Address: rc.Address, Type: rc.Type, Action: rc.Action,
					Reason: "resource type not supported yet",
				})
			}
			continue
		}

		item := LineItem{Address: rc.Address, Type: rc.Type, Action: rc.Action}
		var err error
		switch rc.Action {
		case plan.Create:
			item.After, err = e.priceSide(ctx, pricer, tc, rc, rc.After, rc.AfterUnknown)
		case plan.Delete:
			item.Before, err = e.priceSide(ctx, pricer, tc, rc, rc.Before, nil)
		case plan.NoOp:
			item.Before, err = e.priceSide(ctx, pricer, tc, rc, rc.Before, nil)
			item.After = item.Before
		default: // update, replace
			item.Before, err = e.priceSide(ctx, pricer, tc, rc, rc.Before, nil)
			if err == nil {
				item.After, err = e.priceSide(ctx, pricer, tc, rc, rc.After, rc.AfterUnknown)
			}
		}
		if err != nil {
			reason, ok := UnpricedReason(err)
			if !ok {
				return nil, fmt.Errorf("%s: %w", rc.Address, err)
			}
			rep.Unpriced = append(rep.Unpriced, UnpricedResource{
				Address: rc.Address, Type: rc.Type, Action: rc.Action, Reason: reason,
			})
			continue
		}

		if item.Before != nil {
			rep.TotalBefore = rep.TotalBefore.Add(item.Before.Monthly)
		}
		if item.After != nil {
			rep.TotalAfter = rep.TotalAfter.Add(item.After.Monthly)
		}
		item.Delta = monthly(item.After).Sub(monthly(item.Before))
		rep.Items = append(rep.Items, item)
	}

	rep.Delta = rep.TotalAfter.Sub(rep.TotalBefore)
	rep.PricesAsOf = tc.oldest
	sort.SliceStable(rep.Items, func(i, j int) bool {
		a, b := rep.Items[i].Delta.Abs(), rep.Items[j].Delta.Abs()
		if !a.Equal(b) {
			return a.GreaterThan(b)
		}
		return rep.Items[i].Address < rep.Items[j].Address
	})
	return rep, nil
}

func monthly(e *Estimate) decimal.Decimal {
	if e == nil {
		return decimal.Zero
	}
	return e.Monthly
}

func (e *Estimator) priceSide(ctx context.Context, p ResourcePricer, pc PriceClient, rc plan.ResourceChange, values, unknown map[string]any) (*Estimate, error) {
	attrs := NewAttrs(values, unknown)
	region := e.regionFor(attrs, rc)
	if region == "" {
		return nil, Unpriced("AWS region could not be determined; pass --region or set AWS_REGION")
	}
	return p.Price(ctx, Resource{Type: rc.Type, Address: rc.Address, Region: region, Attrs: attrs}, pc)
}

// regionFor prefers the resource-level `region` argument (AWS provider v6+),
// then the provider configuration, then the estimator default.
func (e *Estimator) regionFor(attrs Attrs, rc plan.ResourceChange) string {
	if r, err := attrs.String("region"); err == nil && r != "" {
		return r
	}
	if rc.Region != "" {
		return rc.Region
	}
	return e.DefaultRegion
}

// trackingClient records the oldest FetchedAt of prices it returns.
type trackingClient struct {
	next   PriceClient
	mu     sync.Mutex
	oldest time.Time
}

func (t *trackingClient) Lookup(ctx context.Context, q Query) (UnitPrice, error) {
	p, err := t.next.Lookup(ctx, q)
	if err == nil && !p.FetchedAt.IsZero() {
		t.mu.Lock()
		if t.oldest.IsZero() || p.FetchedAt.Before(t.oldest) {
			t.oldest = p.FetchedAt
		}
		t.mu.Unlock()
	}
	return p, err
}
