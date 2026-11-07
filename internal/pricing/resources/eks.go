package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

func eksQuery(region, operation, usageType string) pricing.Query {
	return pricing.Query{
		ServiceCode: "AmazonEKS",
		Filters:     map[string]string{"regionCode": region, "operation": operation},
		UsageType:   usageType,
	}
}

func priceEKSCluster(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	a := r.Attrs
	c, err := hourly(ctx, pc, "control plane", "EKS cluster",
		eksQuery(r.Region, "CreateOperation", "AmazonEKS-Hours:perCluster"))
	if err != nil {
		return nil, err
	}
	version, _ := a.String("version")
	summary := "EKS cluster"
	if version != "" {
		summary += " v" + version
	}

	var notes []string
	// EKS defaults to EXTENDED support: once the version leaves standard
	// support, the extended support rate applies instead.
	support, err := a.StringOr("EXTENDED", "upgrade_policy", 0, "support_type")
	if err == nil && support != "STANDARD" {
		if p, err := pc.Lookup(ctx, eksQuery(r.Region, "ExtendedSupport", "AmazonEKS-Hours:extendedSupport")); err == nil {
			notes = append(notes, fmt.Sprintf("extended support (%s/hr, %s/mo) applies instead once the Kubernetes version leaves standard support",
				usd(p.USD), usd(pricing.Monthly(p.USD).Round(2))))
		} else {
			notes = append(notes, "extended support charges apply once the Kubernetes version leaves standard support")
		}
	}
	if auto, err := a.BoolOr(false, "compute_config", 0, "enabled"); err == nil && auto {
		notes = append(notes, "EKS Auto Mode per-instance management fees not included")
	}
	return pricing.NewEstimate(summary, []pricing.Component{c}, notes...), nil
}

func priceEKSNodeGroup(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	a := r.Attrs
	capacity, err := a.StringOr("ON_DEMAND", "capacity_type")
	if err != nil {
		return nil, err
	}
	if capacity == "SPOT" {
		return nil, pricing.Unpriced("spot node groups are not priced (on-demand only)")
	}
	amiType, err := a.StringOr("", "ami_type")
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(amiType, "WINDOWS") {
		return nil, pricing.Unpriced("Windows node groups are not supported (Linux pricing only)")
	}
	types, err := a.Strings("instance_types")
	if err != nil {
		return nil, err
	}
	if len(types) == 0 {
		return nil, pricing.Unpriced("instance_types is not set (launch template instance types are not supported yet)")
	}
	desired, ok, err := a.Number("scaling_config", 0, "desired_size")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, pricing.Unpriced("scaling_config.desired_size is not set")
	}

	instanceType := types[0]
	p, err := pc.Lookup(ctx, ec2InstanceQuery(r.Region, instanceType, "Shared"))
	if err != nil {
		return nil, err
	}
	nodes := pricing.Component{
		Name:    "nodes",
		Monthly: pricing.Monthly(p.USD).Mul(desired),
		Detail:  fmt.Sprintf("%s × %s on-demand Linux @ %s/hr", desired, instanceType, usd(p.USD)),
	}
	notes := []string{"node root volumes not included"}
	if len(types) > 1 {
		notes = append(notes, fmt.Sprintf("priced as %s; other instance types (%s) may cost more or less",
			instanceType, strings.Join(types[1:], ", ")))
	}
	summary := fmt.Sprintf("%s × %s", desired, instanceType)
	return pricing.NewEstimate(summary, []pricing.Component{nodes}, notes...), nil
}
