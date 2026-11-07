package resources

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// rdsEngines maps Terraform engine names to Price List databaseEngine values.
// License-included engines (Oracle, SQL Server) are not supported yet.
var rdsEngines = map[string]string{
	"postgres": "PostgreSQL",
	"mysql":    "MySQL",
	"mariadb":  "MariaDB",
}

// rdsStorage maps storage_type to Price List volumeType and the IOPS
// groupDescription (empty when IOPS are not billed separately).
var rdsStorage = map[string]struct{ volumeType, iopsGroup string }{
	"gp2":      {"General Purpose", ""},
	"gp3":      {"General Purpose-GP3", "RDS Provisioned GP3 IOPS"},
	"io1":      {"Provisioned IOPS", "RDS Provisioned IOPS"},
	"io2":      {"Provisioned IOPS-IO2", "RDS Provisioned IO2 IOPS"},
	"standard": {"Magnetic", ""},
}

// RDS gp3 baseline for MySQL, PostgreSQL and MariaDB: 3,000 IOPS and
// 125 MiB/s below 400 GiB, 12,000 IOPS and 500 MiB/s from 400 GiB.
var (
	rdsGP3LargeVolume   = decimal.NewFromInt(400)
	rdsGP3BaseIOPSSmall = decimal.NewFromInt(3000)
	rdsGP3BaseIOPSLarge = decimal.NewFromInt(12000)
	rdsGP3BaseTputSmall = decimal.NewFromInt(125)
	rdsGP3BaseTputLarge = decimal.NewFromInt(500)
)

func priceDBInstance(ctx context.Context, r pricing.Resource, pc pricing.PriceClient) (*pricing.Estimate, error) {
	a := r.Attrs
	engine, err := a.String("engine")
	if err != nil {
		return nil, err
	}
	dbEngine, ok := rdsEngines[engine]
	if !ok {
		return nil, pricing.Unpriced("engine %q is not supported yet (supported: postgres, mysql, mariadb)", engine)
	}
	class, err := a.String("instance_class")
	if err != nil {
		return nil, err
	}
	if class == "" {
		return nil, pricing.Unpriced("instance_class is not set")
	}
	multiAZ, err := a.BoolOr(false, "multi_az")
	if err != nil {
		return nil, err
	}
	deployment, usagePrefix := "Single-AZ", "InstanceUsage:"
	if multiAZ {
		deployment, usagePrefix = "Multi-AZ", "Multi-AZUsage:"
	}

	compute, err := hourly(ctx, pc, "instance", fmt.Sprintf("%s %s %s", class, dbEngine, deployment), pricing.Query{
		ServiceCode: "AmazonRDS",
		Filters: map[string]string{
			"regionCode":       r.Region,
			"instanceType":     class,
			"databaseEngine":   dbEngine,
			"deploymentOption": deployment,
		},
		UsageType: usagePrefix + class,
	})
	if err != nil {
		return nil, err
	}
	components := []pricing.Component{compute}
	summary := fmt.Sprintf("%s %s", class, dbEngine)
	if multiAZ {
		summary += " Multi-AZ"
	}

	storageComps, storageSummary, notes, err := rdsStorageComponents(ctx, pc, r, dbEngine, deployment)
	if err != nil {
		return nil, err
	}
	components = append(components, storageComps...)
	if storageSummary != "" {
		summary += ", " + storageSummary
	}
	return pricing.NewEstimate(summary, components, notes...), nil
}

func rdsStorageComponents(ctx context.Context, pc pricing.PriceClient, r pricing.Resource, dbEngine, deployment string) ([]pricing.Component, string, []string, error) {
	a := r.Attrs
	storageType, err := a.String("storage_type")
	if err != nil {
		return nil, "", []string{fmt.Sprintf("storage not priced: %v", err)}, nil
	}
	size, ok, err := a.Number("allocated_storage")
	if err != nil {
		return nil, "", []string{fmt.Sprintf("storage not priced: %v", err)}, nil
	}
	if !ok || storageType == "" {
		return nil, "", []string{"storage not priced: allocated_storage or storage_type is not set"}, nil
	}
	st, ok := rdsStorage[storageType]
	if !ok {
		return nil, "", []string{fmt.Sprintf("storage not priced: storage_type %q is not supported", storageType)}, nil
	}

	base := map[string]string{
		"regionCode":       r.Region,
		"databaseEngine":   dbEngine,
		"deploymentOption": deployment,
	}
	with := func(extra map[string]string) map[string]string {
		m := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	sp, err := pc.Lookup(ctx, pricing.Query{
		ServiceCode: "AmazonRDS",
		Filters:     with(map[string]string{"productFamily": "Database Storage", "volumeType": st.volumeType}),
	})
	if err != nil {
		return nil, "", nil, err
	}
	comps := []pricing.Component{{
		Name:    "storage",
		Monthly: size.Mul(sp.USD),
		Detail:  fmt.Sprintf("%s %s GB @ %s/GB-mo", storageType, size, usd(sp.USD)),
	}}
	summary := fmt.Sprintf("%s %s GB", storageType, size)
	var notes []string

	if st.iopsGroup != "" {
		var iops decimal.Decimal
		if storageType == "gp3" {
			iops, err = a.NumberOr(decimal.Zero, "iops") // unset = baseline
		} else {
			iops, _, err = a.Number("iops")
		}
		if err != nil {
			return nil, "", nil, err
		}
		billable := iops
		label := "provisioned"
		if storageType == "gp3" {
			baseIOPS, baseTput := rdsGP3BaseIOPSSmall, rdsGP3BaseTputSmall
			if size.GreaterThanOrEqual(rdsGP3LargeVolume) {
				baseIOPS, baseTput = rdsGP3BaseIOPSLarge, rdsGP3BaseTputLarge
			}
			billable, label = maxZero(iops.Sub(baseIOPS)), "extra"
			if tput, err := a.NumberOr(decimal.Zero, "storage_throughput"); err == nil && tput.GreaterThan(baseTput) {
				notes = append(notes, "gp3 storage throughput above baseline not included")
			}
		}
		if billable.IsPositive() {
			ip, err := pc.Lookup(ctx, pricing.Query{
				ServiceCode: "AmazonRDS",
				Filters:     with(map[string]string{"productFamily": "Provisioned IOPS", "groupDescription": st.iopsGroup}),
			})
			if err != nil {
				return nil, "", nil, err
			}
			comps = append(comps, pricing.Component{
				Name:    "storage IOPS",
				Monthly: billable.Mul(ip.USD),
				Detail:  fmt.Sprintf("%s %s IOPS @ %s/IOPS-mo", label, billable, usd(ip.USD)),
			})
		}
	}
	return comps, summary, notes, nil
}
