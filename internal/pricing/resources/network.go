package resources

import (
	"context"
	"fmt"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

func natQuery(region, groupDescription string) pricing.Query {
	return pricing.Query{
		ServiceCode: "AmazonEC2",
		Filters: map[string]string{
			"productFamily":    "NAT Gateway",
			"regionCode":       region,
			"groupDescription": groupDescription,
		},
	}
}

func priceNATGateway(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	mode, err := r.Attrs.StringOr("zonal", "availability_mode")
	if err != nil {
		return nil, err
	}
	if mode == "regional" {
		return nil, pricing.Unpriced("regional NAT gateways are not supported yet")
	}
	connectivity, err := r.Attrs.StringOr("public", "connectivity_type")
	if err != nil {
		return nil, err
	}
	c, err := hourly(ctx, pc, "gateway", "NAT gateway", natQuery(r.Region, "Hourly charge for NAT Gateways"))
	if err != nil {
		return nil, err
	}
	note := usageNote(ctx, pc, "data processing", natQuery(r.Region, "Charge for per GB data processed by NatGateways"))
	return pricing.NewEstimate("NAT gateway ("+connectivity+")", []pricing.Component{c}, note), nil
}

var lbFamilies = map[string]struct{ family, label string }{
	"application": {"Load Balancer-Application", "Application"},
	"network":     {"Load Balancer-Network", "Network"},
	"gateway":     {"Load Balancer-Gateway", "Gateway"},
}

func lbQuery(region, family, groupDescription string) pricing.Query {
	return pricing.Query{
		ServiceCode: "AWSELB",
		Filters: map[string]string{
			"productFamily":    family,
			"regionCode":       region,
			"groupDescription": groupDescription,
		},
	}
}

func priceLB(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	lbType, err := r.Attrs.StringOr("application", "load_balancer_type")
	if err != nil {
		return nil, err
	}
	f, ok := lbFamilies[lbType]
	if !ok {
		return nil, pricing.Unpriced("load_balancer_type %q is not supported", lbType)
	}
	c, err := hourly(ctx, pc, "load balancer", f.label+" Load Balancer",
		lbQuery(r.Region, f.family, fmt.Sprintf("LoadBalancer hourly usage by %s Load Balancer", f.label)))
	if err != nil {
		return nil, err
	}
	note := usageNote(ctx, pc, "capacity units (LCU)",
		lbQuery(r.Region, f.family, fmt.Sprintf("Used %s Load Balancer capacity units-hr", f.label)))
	return pricing.NewEstimate(f.label+" Load Balancer", []pricing.Component{c}, note), nil
}
