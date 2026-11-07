// Package resources holds the per-resource-type pricers for AWS.
//
// Every pricer assumes on-demand pricing, Linux, and 730 hours per month, and
// prices only fixed or provisioned charges. Usage-based charges (data
// processed, LCUs, requests) are mentioned in notes, never estimated.
package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// Registry returns the pricers for every supported resource type.
func Registry() map[string]pricing.ResourcePricer {
	lb := pricing.PricerFunc(priceLB)
	return map[string]pricing.ResourcePricer{
		"aws_instance":       pricing.PricerFunc(priceInstance),
		"aws_ebs_volume":     pricing.PricerFunc(priceEBSVolume),
		"aws_db_instance":    pricing.PricerFunc(priceDBInstance),
		"aws_nat_gateway":    pricing.PricerFunc(priceNATGateway),
		"aws_lb":             lb,
		"aws_alb":            lb,
		"aws_eks_cluster":    pricing.PricerFunc(priceEKSCluster),
		"aws_eks_node_group": pricing.PricerFunc(priceEKSNodeGroup),
	}
}

// hourly prices an hourly charge as a monthly component.
func hourly(ctx context.Context, pc pricing.PriceClient, name, detail string, q pricing.Query) (pricing.Component, error) {
	p, err := pc.Lookup(ctx, q)
	if err != nil {
		return pricing.Component{}, err
	}
	return pricing.Component{
		Name:    name,
		Monthly: pricing.Monthly(p.USD),
		Detail:  fmt.Sprintf("%s @ %s/hr", detail, usd(p.USD)),
	}, nil
}

// usageNote describes a usage-based charge that is not included, quoting its
// rate when the Price List has it.
func usageNote(ctx context.Context, pc pricing.PriceClient, what string, q pricing.Query) string {
	p, err := pc.Lookup(ctx, q)
	if err != nil {
		return what + " not included"
	}
	return fmt.Sprintf("%s at %s/%s not included", what, usd(p.USD), p.Unit)
}

// usd formats a unit price with at least two decimals: $0.50, $0.0225.
func usd(d decimal.Decimal) string {
	s := d.String() // trailing zeros trimmed
	if _, frac, _ := strings.Cut(s, "."); len(frac) < 2 {
		s = d.StringFixed(2)
	}
	return "$" + s
}

func maxZero(d decimal.Decimal) decimal.Decimal {
	if d.IsNegative() {
		return decimal.Zero
	}
	return d
}
