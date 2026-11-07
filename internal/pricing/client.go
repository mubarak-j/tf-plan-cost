// Package pricing estimates monthly AWS costs for Terraform resource changes.
//
// Prices come from a PriceClient (the AWS Price List Query API in production,
// a fake in tests). Per-resource-type logic lives in ResourcePricers, and the
// Estimator combines both to turn a parsed plan into a Report.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// HoursPerMonth is the billing assumption used for hourly prices.
var HoursPerMonth = decimal.NewFromInt(730)

// Monthly converts an hourly price into a monthly cost.
func Monthly(hourly decimal.Decimal) decimal.Decimal { return hourly.Mul(HoursPerMonth) }

// Query selects exactly one on-demand price from the AWS Price List.
type Query struct {
	ServiceCode string            // e.g. AmazonEC2
	Filters     map[string]string // TERM_MATCH filters on product attributes
	// UsageType, if set, keeps only products whose usagetype equals it or
	// equals "<REGION-PREFIX>-<UsageType>" (e.g. USW2-AmazonEKS-Hours:perCluster).
	UsageType string
}

// Key is a stable identifier for the query, used for caching and fakes.
func (q Query) Key() string {
	keys := make([]string, 0, len(q.Filters))
	for k := range q.Filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(q.ServiceCode)
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%s", k, q.Filters[k])
	}
	if q.UsageType != "" {
		fmt.Fprintf(&b, "|usagetype~%s", q.UsageType)
	}
	return b.String()
}

func (q Query) String() string { return q.Key() }

// UnitPrice is a single on-demand price dimension.
type UnitPrice struct {
	USD         decimal.Decimal `json:"usd"`
	Unit        string          `json:"unit"`
	SKU         string          `json:"sku"`
	Description string          `json:"description"`
	FetchedAt   time.Time       `json:"fetched_at"`
}

// PriceClient looks up a unique on-demand price.
type PriceClient interface {
	Lookup(ctx context.Context, q Query) (UnitPrice, error)
}

// Lookup errors. Both mean "we cannot price this", not "the API failed".
var (
	ErrNoPrice        = errors.New("no matching AWS price")
	ErrAmbiguousPrice = errors.New("more than one matching AWS price")
)

// regionPrefix matches usage type prefixes like USE1, USW2, EU, APN1, EUC1.
var regionPrefix = regexp.MustCompile(`^[A-Z]{2,4}[0-9]?$`)

func usageTypeMatches(usageType, want string) bool {
	if usageType == want {
		return true
	}
	prefix, ok := strings.CutSuffix(usageType, "-"+want)
	return ok && regionPrefix.MatchString(prefix)
}

// selectPrice picks the single on-demand price matching q out of raw Price
// List product documents, as returned by pricing:GetProducts.
func selectPrice(products []string, q Query) (UnitPrice, error) {
	var matches []UnitPrice
	for _, doc := range products {
		var p priceListProduct
		if err := json.Unmarshal([]byte(doc), &p); err != nil {
			return UnitPrice{}, fmt.Errorf("decode price list product: %w", err)
		}
		if q.UsageType != "" && !usageTypeMatches(p.Product.Attributes["usagetype"], q.UsageType) {
			continue
		}
		price, ok, err := p.onDemandPrice()
		if err != nil {
			return UnitPrice{}, fmt.Errorf("%s: %w", q, err)
		}
		if ok {
			matches = append(matches, price)
		}
	}
	switch len(matches) {
	case 0:
		return UnitPrice{}, fmt.Errorf("%w for %s", ErrNoPrice, q)
	case 1:
		return matches[0], nil
	default:
		return UnitPrice{}, fmt.Errorf("%w (%d) for %s", ErrAmbiguousPrice, len(matches), q)
	}
}

type priceListProduct struct {
	Product struct {
		SKU        string            `json:"sku"`
		Attributes map[string]string `json:"attributes"`
	} `json:"product"`
	Terms struct {
		OnDemand map[string]struct {
			PriceDimensions map[string]struct {
				Unit         string            `json:"unit"`
				Description  string            `json:"description"`
				BeginRange   string            `json:"beginRange"`
				PricePerUnit map[string]string `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"OnDemand"`
	} `json:"terms"`
}

// onDemandPrice returns the first-tier on-demand price. Products with no
// on-demand terms (reserved-only) return ok=false.
func (p priceListProduct) onDemandPrice() (UnitPrice, bool, error) {
	var found []UnitPrice
	for _, term := range p.Terms.OnDemand {
		for _, dim := range term.PriceDimensions {
			if dim.BeginRange != "" && dim.BeginRange != "0" {
				continue
			}
			usd, ok := dim.PricePerUnit["USD"]
			if !ok {
				continue
			}
			v, err := decimal.NewFromString(usd)
			if err != nil {
				return UnitPrice{}, false, fmt.Errorf("parse price %q: %w", usd, err)
			}
			found = append(found, UnitPrice{USD: v, Unit: dim.Unit, SKU: p.Product.SKU, Description: dim.Description})
		}
	}
	switch len(found) {
	case 0:
		return UnitPrice{}, false, nil
	case 1:
		return found[0], true, nil
	default:
		return UnitPrice{}, false, fmt.Errorf("%w: sku %s has %d on-demand price dimensions", ErrAmbiguousPrice, p.Product.SKU, len(found))
	}
}
