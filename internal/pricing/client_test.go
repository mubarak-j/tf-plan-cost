package pricing

import (
	"errors"
	"fmt"
	"testing"
)

// product builds a minimal Price List product document.
func product(sku, usageType string, dims ...string) string {
	var pd string
	for i, d := range dims {
		if i > 0 {
			pd += ","
		}
		pd += d
	}
	return fmt.Sprintf(`{"product":{"sku":%q,"attributes":{"usagetype":%q}},
		"terms":{"OnDemand":{"%s.JRTCKXETXF":{"priceDimensions":{%s}}}}}`, sku, usageType, sku, pd)
}

func dim(id, unit, usd, begin string) string {
	return fmt.Sprintf(`%q:{"unit":%q,"description":"d","beginRange":%q,"pricePerUnit":{"USD":%q}}`, id, unit, begin, usd)
}

func TestSelectPrice(t *testing.T) {
	q := Query{ServiceCode: "AmazonEC2"}
	tests := []struct {
		name     string
		products []string
		query    Query
		wantUSD  string
		wantErr  error
	}{
		{
			name:     "single product",
			products: []string{product("A", "BoxUsage:m6i.large", dim("A.1", "Hrs", "0.0960000000", "0"))},
			query:    q,
			wantUSD:  "0.096",
		},
		{
			name:     "no products",
			products: nil,
			query:    q,
			wantErr:  ErrNoPrice,
		},
		{
			name: "two products",
			products: []string{
				product("A", "x", dim("A.1", "Hrs", "1", "0")),
				product("B", "y", dim("B.1", "Hrs", "2", "0")),
			},
			query:   q,
			wantErr: ErrAmbiguousPrice,
		},
		{
			name: "usage type narrows to one, region prefix allowed",
			products: []string{
				product("A", "USE1-AmazonEKS-Hours:perCluster", dim("A.1", "Hours", "0.10", "0")),
				product("B", "USE1-AmazonEKS-Local-Outposts-Hours:perCluster", dim("B.1", "Hours", "0.10", "0")),
			},
			query:   Query{ServiceCode: "AmazonEKS", UsageType: "AmazonEKS-Hours:perCluster"},
			wantUSD: "0.1",
		},
		{
			name: "tiered dimensions use the first tier",
			products: []string{product("A", "x",
				dim("A.1", "GB", "0.09", "0"),
				dim("A.2", "GB", "0.085", "10240"))},
			query:   q,
			wantUSD: "0.09",
		},
		{
			name:     "product without on-demand terms is ignored",
			products: []string{`{"product":{"sku":"R","attributes":{}},"terms":{"Reserved":{}}}`},
			query:    q,
			wantErr:  ErrNoPrice,
		},
		{
			name: "two first-tier dimensions are ambiguous",
			products: []string{product("A", "x",
				dim("A.1", "Hrs", "1", "0"),
				dim("A.2", "Hrs", "2", ""))},
			query:   q,
			wantErr: ErrAmbiguousPrice,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := selectPrice(tt.products, tt.query)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.USD.String() != tt.wantUSD {
				t.Errorf("USD = %s, want %s", p.USD, tt.wantUSD)
			}
		})
	}
}

func TestSelectPriceBadJSON(t *testing.T) {
	if _, err := selectPrice([]string{"{"}, Query{}); err == nil {
		t.Fatal("want error")
	}
}

func TestUsageTypeMatches(t *testing.T) {
	tests := []struct {
		usageType, want string
		match           bool
	}{
		{"InstanceUsage:db.t4g.micro", "InstanceUsage:db.t4g.micro", true},
		{"USW2-InstanceUsage:db.t4g.micro", "InstanceUsage:db.t4g.micro", true},
		{"EU-InstanceUsage:db.t4g.micro", "InstanceUsage:db.t4g.micro", true},
		{"APN1-InstanceUsage:db.t4g.micro", "InstanceUsage:db.t4g.micro", true},
		{"UnusedBox:db.t4g.micro", "InstanceUsage:db.t4g.micro", false},
		{"USE1-AmazonEKS-Local-Outposts-Hours:perCluster", "AmazonEKS-Hours:perCluster", false},
		{"EU-Outposts-LoadBalancerUsage", "LoadBalancerUsage", false},
		{"Outposts-LoadBalancerUsage", "LoadBalancerUsage", false},
	}
	for _, tt := range tests {
		if got := usageTypeMatches(tt.usageType, tt.want); got != tt.match {
			t.Errorf("usageTypeMatches(%q, %q) = %v, want %v", tt.usageType, tt.want, got, tt.match)
		}
	}
}

func TestQueryKeyIsStable(t *testing.T) {
	a := Query{ServiceCode: "AmazonEC2", Filters: map[string]string{"b": "2", "a": "1", "c": "3"}}
	b := Query{ServiceCode: "AmazonEC2", Filters: map[string]string{"c": "3", "a": "1", "b": "2"}}
	if a.Key() != b.Key() {
		t.Errorf("keys differ: %q vs %q", a.Key(), b.Key())
	}
	if want := "AmazonEC2|a=1|b=2|c=3"; a.Key() != want {
		t.Errorf("Key() = %q, want %q", a.Key(), want)
	}
	c := a
	c.UsageType = "X"
	if c.Key() == a.Key() {
		t.Error("UsageType must change the key")
	}
}
