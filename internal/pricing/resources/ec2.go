package resources

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// gp3 includes this much performance in the storage price.
var (
	gp3BaseIOPS       = decimal.NewFromInt(3000)
	gp3BaseThroughput = decimal.NewFromInt(125) // MiB/s
	io2Tier1Max       = decimal.NewFromInt(32000)
	io2Tier2Max       = decimal.NewFromInt(64000)
	mibPerGiB         = decimal.NewFromInt(1024)
)

func ec2InstanceQuery(region, instanceType, tenancy string) pricing.Query {
	return pricing.Query{
		ServiceCode: "AmazonEC2",
		Filters: map[string]string{
			"regionCode":      region,
			"instanceType":    instanceType,
			"operatingSystem": "Linux",
			"tenancy":         tenancy,
			"preInstalledSw":  "NA",
			"capacitystatus":  "Used",
			"licenseModel":    "No License required",
		},
	}
}

func priceInstance(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	a := r.Attrs
	instanceType, err := a.String("instance_type")
	if err != nil {
		return nil, err
	}
	if instanceType == "" {
		return nil, pricing.Unpriced("instance_type is not set (launch template instances are not supported yet)")
	}
	market, err := a.StringOr("", "instance_market_options", 0, "market_type")
	if err != nil {
		return nil, err
	}
	if market == "spot" {
		return nil, pricing.Unpriced("spot instances are not priced (on-demand only)")
	}

	tenancy, err := a.StringOr("default", "tenancy")
	if err != nil {
		return nil, err
	}
	var priceTenancy string
	switch tenancy {
	case "", "default":
		priceTenancy = "Shared"
	case "dedicated":
		priceTenancy = "Dedicated"
	default:
		return nil, pricing.Unpriced("tenancy %q is not supported (dedicated hosts are billed per host)", tenancy)
	}

	compute, err := hourly(ctx, pc, "compute", instanceType+" on-demand Linux",
		ec2InstanceQuery(r.Region, instanceType, priceTenancy))
	if err != nil {
		return nil, err
	}
	components := []pricing.Component{compute}
	var notes []string

	// Root and additional EBS volumes declared inline.
	blocks := []struct {
		attr string
		name func(i int) string
	}{
		{"root_block_device", func(int) string { return "root volume" }},
		{"ebs_block_device", func(i int) string {
			dev, _ := a.String("ebs_block_device", i, "device_name")
			return strings.TrimSpace("ebs volume " + dev)
		}},
	}
	for _, blk := range blocks {
		n, err := a.Len(blk.attr)
		if err != nil {
			// An unset block list is "known after apply" (filled from the
			// AMI); only note it for the root volume.
			if blk.attr == "root_block_device" {
				notes = append(notes, fmt.Sprintf("root volume not priced: %v", err))
			}
			continue
		}
		for i := range n {
			name := blk.name(i)
			v, err := blockVolume(a, blk.attr, i)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s not priced: %v", name, err))
				continue
			}
			comps, volNotes, err := ebsComponents(ctx, pc, r.Region, name, v)
			if err != nil {
				if reason, ok := pricing.UnpricedReason(err); ok {
					notes = append(notes, fmt.Sprintf("%s not priced: %s", name, reason))
					continue
				}
				return nil, err
			}
			components = append(components, comps...)
			notes = append(notes, volNotes...)
		}
	}
	return pricing.NewEstimate(instanceType, components, notes...), nil
}

func priceEBSVolume(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	v, err := readVolume(r.Attrs, "type", "size", "iops", "throughput")
	if err != nil {
		return nil, err
	}
	comps, notes, err := ebsComponents(ctx, pc, r.Region, "storage", v)
	if err != nil {
		return nil, err
	}
	return pricing.NewEstimate(v.summary(), comps, notes...), nil
}

type volume struct {
	Type       string
	SizeGB     decimal.Decimal
	IOPS       decimal.Decimal
	Throughput decimal.Decimal // MiB/s
}

func (v volume) summary() string { return fmt.Sprintf("%s %s GB", v.Type, v.SizeGB) }

func blockVolume(a pricing.Attrs, attr string, i int) (volume, error) {
	sub := func(k string) []any { return []any{attr, i, k} }
	return readVolumeAt(a, sub("volume_type"), sub("volume_size"), sub("iops"), sub("throughput"))
}

func readVolume(a pricing.Attrs, typeKey, sizeKey, iopsKey, tputKey string) (volume, error) {
	return readVolumeAt(a, []any{typeKey}, []any{sizeKey}, []any{iopsKey}, []any{tputKey})
}

func readVolumeAt(a pricing.Attrs, typePath, sizePath, iopsPath, tputPath []any) (volume, error) {
	var v volume
	var err error
	if v.Type, err = a.String(typePath...); err != nil {
		return v, err
	}
	if v.Type == "" {
		return v, pricing.Unpriced("volume type is not set")
	}
	size, ok, err := a.Number(sizePath...)
	if err != nil {
		return v, err
	}
	if !ok {
		return v, pricing.Unpriced("volume size is not set")
	}
	v.SizeGB = size
	// io1/io2 bill every provisioned IOPS, so the value must be known. For
	// gp3 an unset IOPS or throughput means the included baseline.
	if v.Type == "io1" || v.Type == "io2" {
		if v.IOPS, _, err = a.Number(iopsPath...); err != nil {
			return v, err
		}
	} else if v.IOPS, err = a.NumberOr(decimal.Zero, iopsPath...); err != nil {
		return v, err
	}
	if v.Throughput, err = a.NumberOr(decimal.Zero, tputPath...); err != nil {
		return v, err
	}
	return v, nil
}

// ebsComponents prices an EBS volume's storage and provisioned performance.
func ebsComponents(ctx context.Context, pc pricing.PriceClient, region, name string, v volume) ([]pricing.Component, []string, error) {
	switch v.Type {
	case "gp2", "gp3", "io1", "io2", "st1", "sc1", "standard":
	default:
		return nil, nil, pricing.Unpriced("EBS volume type %q is not supported", v.Type)
	}
	storage, err := pc.Lookup(ctx, pricing.Query{
		ServiceCode: "AmazonEC2",
		Filters: map[string]string{
			"productFamily": "Storage",
			"volumeApiName": v.Type,
			"regionCode":    region,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	comps := []pricing.Component{{
		Name:    name,
		Monthly: v.SizeGB.Mul(storage.USD),
		Detail:  fmt.Sprintf("%s %s GB @ %s/GB-mo", v.Type, v.SizeGB, usd(storage.USD)),
	}}
	var notes []string

	iopsComp := func(group string, iops decimal.Decimal, label string) error {
		if !iops.IsPositive() {
			return nil
		}
		p, err := pc.Lookup(ctx, pricing.Query{
			ServiceCode: "AmazonEC2",
			Filters: map[string]string{
				"productFamily": "System Operation",
				"volumeApiName": v.Type,
				"group":         group,
				"regionCode":    region,
			},
		})
		if err != nil {
			return err
		}
		comps = append(comps, pricing.Component{
			Name:    name + " IOPS",
			Monthly: iops.Mul(p.USD),
			Detail:  fmt.Sprintf("%s %s IOPS @ %s/IOPS-mo", label, iops, usd(p.USD)),
		})
		return nil
	}

	switch v.Type {
	case "gp3":
		if err := iopsComp("EBS IOPS", maxZero(v.IOPS.Sub(gp3BaseIOPS)), "extra"); err != nil {
			return nil, nil, err
		}
		extra := maxZero(v.Throughput.Sub(gp3BaseThroughput))
		if extra.IsPositive() {
			p, err := pc.Lookup(ctx, pricing.Query{
				ServiceCode: "AmazonEC2",
				Filters: map[string]string{
					"productFamily": "Provisioned Throughput",
					"volumeApiName": "gp3",
					"group":         "EBS Throughput",
					"regionCode":    region,
				},
			})
			if err != nil {
				return nil, nil, err
			}
			// The Price List quotes gp3 throughput per GiBps-month.
			perMiB := p.USD
			switch strings.ToLower(p.Unit) {
			case "gibps-mo":
				perMiB = p.USD.Div(mibPerGiB)
			case "mibps-mo":
			default:
				return nil, nil, pricing.Unpriced("unexpected gp3 throughput price unit %q", p.Unit)
			}
			comps = append(comps, pricing.Component{
				Name:    name + " throughput",
				Monthly: extra.Mul(perMiB),
				Detail:  fmt.Sprintf("extra %s MiB/s @ %s/MiBps-mo", extra, usd(perMiB)),
			})
		}
	case "io1":
		if err := iopsComp("EBS IOPS", v.IOPS, "provisioned"); err != nil {
			return nil, nil, err
		}
	case "io2":
		tier1 := decimal.Min(v.IOPS, io2Tier1Max)
		tier2 := maxZero(decimal.Min(v.IOPS, io2Tier2Max).Sub(io2Tier1Max))
		tier3 := maxZero(v.IOPS.Sub(io2Tier2Max))
		for _, t := range []struct {
			group string
			iops  decimal.Decimal
			label string
		}{
			{"EBS IOPS", tier1, "tier 1"},
			{"EBS IOPS Tier 2", tier2, "tier 2"},
			{"EBS IOPS Tier 3", tier3, "tier 3"},
		} {
			if err := iopsComp(t.group, t.iops, t.label); err != nil {
				return nil, nil, err
			}
		}
	case "standard":
		notes = append(notes, "magnetic volume I/O requests not included")
	}
	return comps, notes, nil
}
