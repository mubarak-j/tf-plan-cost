package pricing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// fakePricer prices a "fake_thing" at its `cost` attribute, records the
// region it was asked for, and fails on demand via the `fail` attribute.
func fakePricer(regions *[]string) pricing.ResourcePricer {
	return pricing.PricerFunc(func(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
		*regions = append(*regions, r.Address+"@"+r.Region)
		switch fail, _ := r.Attrs.String("fail"); fail {
		case "unpriced":
			return nil, pricing.Unpriced("cannot price")
		case "noprice":
			return nil, fmt.Errorf("%w for test", pricing.ErrNoPrice)
		case "api":
			return nil, errors.New("connection refused")
		}
		if _, err := pc.Lookup(ctx, pricing.Query{ServiceCode: r.Address}); err != nil {
			return nil, err
		}
		cost, _, err := r.Attrs.Number("cost")
		if err != nil {
			return nil, err
		}
		name, _ := r.Attrs.String("name")
		return pricing.NewEstimate(name, []pricing.Component{{Name: "x", Monthly: cost}}), nil
	})
}

// datedClient returns prices fetched at different times per query.
type datedClient map[string]time.Time

func (d datedClient) Lookup(_ context.Context, q pricing.Query) (pricing.UnitPrice, error) {
	return pricing.UnitPrice{FetchedAt: d[q.ServiceCode]}, nil
}

func attrs(cost string, extra ...string) map[string]any {
	m := map[string]any{"cost": json.Number(cost), "name": "n" + cost}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

func change(addr string, action plan.Action, before, after map[string]any) plan.ResourceChange {
	return plan.ResourceChange{Address: addr, Type: "fake_thing", Action: action, Before: before, After: after, Region: "us-east-1"}
}

func TestEstimate(t *testing.T) {
	var regions []string
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	e := pricing.Estimator{
		Client:        datedClient{"c": day(5), "u": day(3), "r": day(6)},
		Pricers:       map[string]pricing.ResourcePricer{"fake_thing": fakePricer(&regions)},
		DefaultRegion: "eu-west-1",
	}
	p := &plan.Plan{Changes: []plan.ResourceChange{
		change("c", plan.Create, nil, attrs("100")),
		change("u", plan.Update, attrs("50"), attrs("80")),
		change("d", plan.Delete, attrs("30"), nil),
		change("r", plan.Replace, attrs("10"), attrs("5")),
		change("n", plan.NoOp, attrs("7"), attrs("7")),
		change("f", plan.Forget, attrs("1000"), nil),
		change("x1", plan.Create, nil, attrs("1", "fail", "unpriced")),
		change("x2", plan.Update, attrs("1"), attrs("2", "fail", "noprice")),
		{Address: "s3", Type: "aws_s3_bucket", Action: plan.Update},
		{Address: "iam", Type: "aws_iam_role", Action: plan.NoOp},
	}}
	// "d" and "n" get a zero FetchedAt, which must not count as the oldest.
	rep, err := e.Estimate(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	// before = 50 + 30 + 10 + 7 = 97; after = 100 + 80 + 5 + 7 = 192
	check := func(name string, got decimal.Decimal, want string) {
		t.Helper()
		if !got.Equal(decimal.RequireFromString(want)) {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	check("TotalBefore", rep.TotalBefore, "97")
	check("TotalAfter", rep.TotalAfter, "192")
	check("Delta", rep.Delta, "95")

	// Sorted by |delta| desc, then address; forget is skipped.
	var order []string
	for _, it := range rep.Items {
		order = append(order, fmt.Sprintf("%s:%s", it.Address, it.Delta))
	}
	if got, want := strings.Join(order, " "), "c:100 d:-30 u:30 r:-5 n:0"; got != want {
		t.Errorf("items = %s, want %s", got, want)
	}
	if got := len(rep.Changed()); got != 4 {
		t.Errorf("Changed() = %d items, want 4", got)
	}

	if len(rep.Unpriced) != 2 || rep.Unpriced[0].Address != "x1" || rep.Unpriced[0].Reason != "cannot price" ||
		rep.Unpriced[1].Address != "x2" || !strings.Contains(rep.Unpriced[1].Reason, "no matching AWS price") {
		t.Errorf("Unpriced = %+v", rep.Unpriced)
	}
	if len(rep.Unsupported) != 1 || rep.Unsupported[0].Address != "s3" {
		t.Errorf("Unsupported = %+v (unchanged unsupported resources must be ignored)", rep.Unsupported)
	}
	if !rep.PricesAsOf.Equal(day(3)) {
		t.Errorf("PricesAsOf = %v, want oldest fetch %v", rep.PricesAsOf, day(3))
	}
}

func TestEstimateRegion(t *testing.T) {
	var regions []string
	e := pricing.Estimator{
		Client:        datedClient{},
		Pricers:       map[string]pricing.ResourcePricer{"fake_thing": fakePricer(&regions)},
		DefaultRegion: "eu-west-1",
	}
	withAttr := change("attr", plan.Create, nil, attrs("1", "region", "ap-south-1"))
	fromConfig := change("config", plan.Create, nil, attrs("1"))
	fromConfig.Region = "us-west-2"
	fallback := change("default", plan.Create, nil, attrs("1"))
	fallback.Region = ""

	if _, err := e.Estimate(context.Background(), &plan.Plan{Changes: []plan.ResourceChange{withAttr, fromConfig, fallback}}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(regions, " "), "attr@ap-south-1 config@us-west-2 default@eu-west-1"; got != want {
		t.Errorf("regions = %s, want %s", got, want)
	}

	e.DefaultRegion = ""
	rep, err := e.Estimate(context.Background(), &plan.Plan{Changes: []plan.ResourceChange{fallback}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unpriced) != 1 || !strings.Contains(rep.Unpriced[0].Reason, "region could not be determined") {
		t.Errorf("Unpriced = %+v", rep.Unpriced)
	}
}

func TestEstimateAPIErrorFails(t *testing.T) {
	var regions []string
	e := pricing.Estimator{
		Client:  datedClient{},
		Pricers: map[string]pricing.ResourcePricer{"fake_thing": fakePricer(&regions)},
	}
	_, err := e.Estimate(context.Background(), &plan.Plan{Changes: []plan.ResourceChange{
		change("broken", plan.Create, nil, attrs("1", "fail", "api")),
	}})
	if err == nil || !strings.Contains(err.Error(), "broken: connection refused") {
		t.Errorf("err = %v, want API error with address", err)
	}
}
