package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/pricingtest"
)

const region = "us-east-1"

// Expected Price List queries, written out literally so a filter typo in a
// pricer fails the test instead of silently matching.

func ec2Q(instanceType, tenancy string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEC2", Filters: map[string]string{
		"regionCode": region, "instanceType": instanceType, "operatingSystem": "Linux", "tenancy": tenancy,
		"preInstalledSw": "NA", "capacitystatus": "Used", "licenseModel": "No License required",
	}}
}

func ebsStorageQ(volType string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEC2", Filters: map[string]string{
		"productFamily": "Storage", "volumeApiName": volType, "regionCode": region,
	}}
}

func ebsIOPSQ(volType, group string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEC2", Filters: map[string]string{
		"productFamily": "System Operation", "volumeApiName": volType, "group": group, "regionCode": region,
	}}
}

func gp3ThroughputQ() pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEC2", Filters: map[string]string{
		"productFamily": "Provisioned Throughput", "volumeApiName": "gp3", "group": "EBS Throughput", "regionCode": region,
	}}
}

func rdsInstanceQ(class, engine, deployment, usageType string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonRDS", Filters: map[string]string{
		"regionCode": region, "instanceType": class, "databaseEngine": engine, "deploymentOption": deployment,
	}, UsageType: usageType}
}

func rdsStorageQ(engine, deployment, volumeType string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonRDS", Filters: map[string]string{
		"regionCode": region, "databaseEngine": engine, "deploymentOption": deployment,
		"productFamily": "Database Storage", "volumeType": volumeType,
	}}
}

func rdsIOPSQ(engine, deployment, group string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonRDS", Filters: map[string]string{
		"regionCode": region, "databaseEngine": engine, "deploymentOption": deployment,
		"productFamily": "Provisioned IOPS", "groupDescription": group,
	}}
}

func natQ(groupDescription string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEC2", Filters: map[string]string{
		"productFamily": "NAT Gateway", "regionCode": region, "groupDescription": groupDescription,
	}}
}

func lbQ(family, groupDescription string) pricing.Query {
	return pricing.Query{ServiceCode: "AWSELB", Filters: map[string]string{
		"productFamily": family, "regionCode": region, "groupDescription": groupDescription,
	}}
}

func eksQ(operation, usageType string) pricing.Query {
	return pricing.Query{ServiceCode: "AmazonEKS", Filters: map[string]string{
		"regionCode": region, "operation": operation,
	}, UsageType: usageType}
}

// prices are real us-east-1 list prices (November 2026).
func fakePrices() *pricingtest.Client {
	return pricingtest.New().
		Set(ec2Q("m6i.large", "Shared"), "0.096", "Hrs").
		Set(ec2Q("m6i.large", "Dedicated"), "0.1056", "Hrs").
		Set(ebsStorageQ("gp2"), "0.10", "GB-Mo").
		Set(ebsStorageQ("gp3"), "0.08", "GB-Mo").
		Set(ebsStorageQ("io1"), "0.125", "GB-Mo").
		Set(ebsStorageQ("io2"), "0.125", "GB-Mo").
		Set(ebsStorageQ("standard"), "0.05", "GB-Mo").
		Set(ebsIOPSQ("gp3", "EBS IOPS"), "0.005", "IOPS-Mo").
		Set(ebsIOPSQ("io1", "EBS IOPS"), "0.065", "IOPS-Mo").
		Set(ebsIOPSQ("io2", "EBS IOPS"), "0.065", "IOPS-Mo").
		Set(ebsIOPSQ("io2", "EBS IOPS Tier 2"), "0.0455", "IOPS-Mo").
		Set(ebsIOPSQ("io2", "EBS IOPS Tier 3"), "0.03185", "IOPS-Mo").
		Set(gp3ThroughputQ(), "40.96", "GiBps-mo").
		Set(rdsInstanceQ("db.t4g.micro", "PostgreSQL", "Single-AZ", "InstanceUsage:db.t4g.micro"), "0.016", "Hrs").
		Set(rdsInstanceQ("db.m6g.large", "MySQL", "Multi-AZ", "Multi-AZUsage:db.m6g.large"), "0.304", "Hrs").
		Set(rdsStorageQ("PostgreSQL", "Single-AZ", "General Purpose"), "0.115", "GB-Mo").
		Set(rdsStorageQ("PostgreSQL", "Single-AZ", "General Purpose-GP3"), "0.115", "GB-Mo").
		Set(rdsStorageQ("PostgreSQL", "Single-AZ", "Provisioned IOPS"), "0.125", "GB-Mo").
		Set(rdsStorageQ("MySQL", "Multi-AZ", "General Purpose-GP3"), "0.23", "GB-Mo").
		Set(rdsIOPSQ("PostgreSQL", "Single-AZ", "RDS Provisioned IOPS"), "0.10", "IOPS-Mo").
		Set(rdsIOPSQ("MySQL", "Multi-AZ", "RDS Provisioned GP3 IOPS"), "0.04", "IOPS-Mo").
		Set(natQ("Hourly charge for NAT Gateways"), "0.045", "Hrs").
		Set(natQ("Charge for per GB data processed by NatGateways"), "0.045", "GB").
		Set(lbQ("Load Balancer-Application", "LoadBalancer hourly usage by Application Load Balancer"), "0.0225", "Hrs").
		Set(lbQ("Load Balancer-Application", "Used Application Load Balancer capacity units-hr"), "0.008", "LCU-Hrs").
		Set(lbQ("Load Balancer-Network", "LoadBalancer hourly usage by Network Load Balancer"), "0.0225", "Hrs").
		Set(lbQ("Load Balancer-Network", "Used Network Load Balancer capacity units-hr"), "0.006", "LCU-Hrs").
		Set(lbQ("Load Balancer-Gateway", "LoadBalancer hourly usage by Gateway Load Balancer"), "0.0125", "Hrs").
		Set(lbQ("Load Balancer-Gateway", "Used Gateway Load Balancer capacity units-hr"), "0.004", "LCU-Hrs").
		Set(eksQ("CreateOperation", "AmazonEKS-Hours:perCluster"), "0.10", "Hours").
		Set(eksQ("ExtendedSupport", "AmazonEKS-Hours:extendedSupport"), "0.50", "Hours")
}

type pricerTest struct {
	name         string
	typ          string
	values       string // JSON attributes
	unknown      string // JSON after_unknown
	prices       *pricingtest.Client
	wantMonthly  string
	wantSummary  string
	wantNotes    []string // substrings, in order
	wantUnpriced string   // substring of the unpriced reason
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	if s == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m
}

func runPricerTests(t *testing.T, tests []pricerTest) {
	t.Helper()
	reg := Registry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := tt.prices
			if pc == nil {
				pc = fakePrices()
			}
			r := pricing.Resource{
				Type: tt.typ, Address: tt.typ + ".test", Region: region,
				Attrs: pricing.NewAttrs(decode(t, tt.values), decode(t, tt.unknown)),
			}
			est, err := reg[tt.typ].Price(context.Background(), r, pc)
			if tt.wantUnpriced != "" {
				reason, ok := pricing.UnpricedReason(err)
				if !ok || !strings.Contains(reason, tt.wantUnpriced) {
					t.Fatalf("err = %v, want unpriced containing %q", err, tt.wantUnpriced)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !est.Monthly.Equal(decimal.RequireFromString(tt.wantMonthly)) {
				t.Errorf("Monthly = %s, want %s; components: %+v", est.Monthly, tt.wantMonthly, est.Components)
			}
			if tt.wantSummary != "" && est.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", est.Summary, tt.wantSummary)
			}
			if len(est.Notes) != len(tt.wantNotes) {
				t.Fatalf("Notes = %q, want %d notes like %q", est.Notes, len(tt.wantNotes), tt.wantNotes)
			}
			for i, want := range tt.wantNotes {
				if !strings.Contains(est.Notes[i], want) {
					t.Errorf("Notes[%d] = %q, want it to contain %q", i, est.Notes[i], want)
				}
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	want := []string{"aws_instance", "aws_ebs_volume", "aws_db_instance", "aws_nat_gateway", "aws_lb", "aws_alb", "aws_eks_cluster", "aws_eks_node_group"}
	reg := Registry()
	if len(reg) != len(want) {
		t.Errorf("Registry has %d types, want %d", len(reg), len(want))
	}
	for _, typ := range want {
		if reg[typ] == nil {
			t.Errorf("missing pricer for %s", typ)
		}
	}
}

func TestInstance(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{
			name:        "on-demand with gp3 root volume",
			typ:         "aws_instance",
			values:      `{"instance_type": "m6i.large", "tenancy": "default", "root_block_device": [{"volume_size": 30, "volume_type": "gp3", "iops": 3000, "throughput": 125}], "ebs_block_device": []}`,
			wantMonthly: "72.48", // 0.096*730 + 30*0.08
			wantSummary: "m6i.large",
		},
		{
			name:        "unset optional attributes use AWS defaults",
			typ:         "aws_instance",
			values:      `{"instance_type": "m6i.large", "root_block_device": [{"volume_size": 30, "volume_type": "gp3"}]}`,
			unknown:     `{"tenancy": true, "instance_market_options": true, "ebs_block_device": true, "root_block_device": [{"iops": true, "throughput": true}]}`,
			wantMonthly: "72.48",
		},
		{
			name:        "root volume size unknown is a note, not a failure",
			typ:         "aws_instance",
			values:      `{"instance_type": "m6i.large"}`,
			unknown:     `{"root_block_device": true}`,
			wantMonthly: "70.08",
			wantNotes:   []string{"root volume not priced: root_block_device is known only after apply"},
		},
		{
			name:        "additional io1 volume",
			typ:         "aws_instance",
			values:      `{"instance_type": "m6i.large", "ebs_block_device": [{"device_name": "/dev/sdf", "volume_size": 100, "volume_type": "io1", "iops": 1000}]}`,
			wantMonthly: "147.58", // 70.08 + 100*0.125 + 1000*0.065
		},
		{
			name:        "dedicated tenancy",
			typ:         "aws_instance",
			values:      `{"instance_type": "m6i.large", "tenancy": "dedicated"}`,
			wantMonthly: "77.088",
		},
		{name: "dedicated host", typ: "aws_instance", values: `{"instance_type": "m6i.large", "tenancy": "host"}`, wantUnpriced: "billed per host"},
		{name: "spot", typ: "aws_instance", values: `{"instance_type": "m6i.large", "instance_market_options": [{"market_type": "spot"}]}`, wantUnpriced: "spot"},
		{name: "unknown instance type", typ: "aws_instance", values: `{}`, unknown: `{"instance_type": true}`, wantUnpriced: "instance_type is known only after apply"},
		{name: "launch template", typ: "aws_instance", values: `{"launch_template": [{"id": "lt-1"}]}`, wantUnpriced: "instance_type is not set"},
		{
			name:         "instance type not in price list",
			typ:          "aws_instance",
			values:       `{"instance_type": "m99.huge"}`,
			prices:       fakePrices().SetNoPrice(ec2Q("m99.huge", "Shared")),
			wantUnpriced: "no matching AWS price",
		},
	})
}

func TestEBSVolume(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{name: "gp2", typ: "aws_ebs_volume", values: `{"type": "gp2", "size": 100}`, wantMonthly: "10", wantSummary: "gp2 100 GB"},
		{
			name:        "gp3 baseline (unset iops/throughput)",
			typ:         "aws_ebs_volume",
			values:      `{"type": "gp3", "size": 500}`,
			unknown:     `{"iops": true, "throughput": true}`,
			wantMonthly: "40",
		},
		{
			// 500*0.08 + (6000-3000)*0.005 + (250-125)/1024*40.96
			name:        "gp3 with extra IOPS and throughput (GiBps price converted to MiBps)",
			typ:         "aws_ebs_volume",
			values:      `{"type": "gp3", "size": 500, "iops": 6000, "throughput": 250}`,
			wantMonthly: "60",
		},
		{
			name:         "gp3 throughput in an unexpected unit",
			typ:          "aws_ebs_volume",
			values:       `{"type": "gp3", "size": 500, "throughput": 250}`,
			prices:       fakePrices().Set(gp3ThroughputQ(), "0.04", "Gbps-hrs"),
			wantUnpriced: `unexpected gp3 throughput price unit "Gbps-hrs"`,
		},
		{
			// 200*0.125 + 32000*0.065 + 32000*0.0455 + 6000*0.03185
			name:        "io2 tiered IOPS",
			typ:         "aws_ebs_volume",
			values:      `{"type": "io2", "size": 200, "iops": 70000}`,
			wantMonthly: "3752.1",
		},
		{name: "io2 tier 1 only", typ: "aws_ebs_volume", values: `{"type": "io2", "size": 100, "iops": 1000}`, wantMonthly: "77.5"},
		{name: "magnetic notes I/O", typ: "aws_ebs_volume", values: `{"type": "standard", "size": 100}`, wantMonthly: "5", wantNotes: []string{"I/O requests not included"}},
		{name: "io1 IOPS unknown", typ: "aws_ebs_volume", values: `{"type": "io1", "size": 100}`, unknown: `{"iops": true}`, wantUnpriced: "iops is known only after apply"},
		{name: "size from snapshot", typ: "aws_ebs_volume", values: `{"type": "gp3"}`, unknown: `{"size": true}`, wantUnpriced: "size is known only after apply"},
		{name: "unsupported type", typ: "aws_ebs_volume", values: `{"type": "gp9", "size": 1}`, wantUnpriced: `EBS volume type "gp9" is not supported`},
	})
}

func TestDBInstance(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{
			name:        "postgres single-AZ gp2",
			typ:         "aws_db_instance",
			values:      `{"engine": "postgres", "instance_class": "db.t4g.micro", "multi_az": false, "storage_type": "gp2", "allocated_storage": 20}`,
			wantMonthly: "13.98", // 0.016*730 + 20*0.115
			wantSummary: "db.t4g.micro PostgreSQL, gp2 20 GB",
		},
		{
			name:        "gp3 below 400 GB with unset IOPS has no IOPS charge",
			typ:         "aws_db_instance",
			values:      `{"engine": "postgres", "instance_class": "db.t4g.micro", "storage_type": "gp3", "allocated_storage": 100}`,
			unknown:     `{"iops": true, "storage_throughput": true, "multi_az": true}`,
			wantMonthly: "23.18", // 11.68 + 100*0.115
		},
		{
			// 0.304*730 + 500*0.23 + (15000-12000)*0.04
			name:        "mysql multi-AZ gp3 above baseline",
			typ:         "aws_db_instance",
			values:      `{"engine": "mysql", "instance_class": "db.m6g.large", "multi_az": true, "storage_type": "gp3", "allocated_storage": 500, "iops": 15000, "storage_throughput": 600}`,
			wantMonthly: "456.92",
			wantSummary: "db.m6g.large MySQL Multi-AZ, gp3 500 GB",
			wantNotes:   []string{"throughput above baseline not included"},
		},
		{
			name:        "io1 provisioned IOPS",
			typ:         "aws_db_instance",
			values:      `{"engine": "postgres", "instance_class": "db.t4g.micro", "storage_type": "io1", "allocated_storage": 100, "iops": 1000}`,
			wantMonthly: "124.18", // 11.68 + 100*0.125 + 1000*0.10
		},
		{
			name:        "unknown storage is a note",
			typ:         "aws_db_instance",
			values:      `{"engine": "postgres", "instance_class": "db.t4g.micro", "allocated_storage": 20}`,
			unknown:     `{"storage_type": true}`,
			wantMonthly: "11.68",
			wantNotes:   []string{"storage not priced: storage_type is known only after apply"},
		},
		{name: "oracle", typ: "aws_db_instance", values: `{"engine": "oracle-ee", "instance_class": "db.m6i.large"}`, wantUnpriced: `engine "oracle-ee" is not supported`},
		{name: "aurora", typ: "aws_db_instance", values: `{"engine": "aurora-postgresql", "instance_class": "db.r6g.large"}`, wantUnpriced: "not supported"},
		{name: "unknown class", typ: "aws_db_instance", values: `{"engine": "mysql"}`, unknown: `{"instance_class": true}`, wantUnpriced: "instance_class is known only after apply"},
	})
}

func TestNATGateway(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{
			name:        "zonal public",
			typ:         "aws_nat_gateway",
			values:      `{"connectivity_type": "public", "availability_mode": "zonal"}`,
			wantMonthly: "32.85",
			wantSummary: "NAT gateway (public)",
			wantNotes:   []string{"data processing at $0.045/GB not included"},
		},
		{
			name:        "defaults when unset",
			typ:         "aws_nat_gateway",
			values:      `{}`,
			unknown:     `{"availability_mode": true, "connectivity_type": true}`,
			wantMonthly: "32.85",
			wantSummary: "NAT gateway (public)",
			wantNotes:   []string{"data processing"},
		},
		{
			name:        "usage note without a rate when the rate is missing",
			typ:         "aws_nat_gateway",
			values:      `{}`,
			prices:      fakePrices().SetNoPrice(natQ("Charge for per GB data processed by NatGateways")),
			wantMonthly: "32.85",
			wantNotes:   []string{"data processing not included"},
		},
		{name: "regional", typ: "aws_nat_gateway", values: `{"availability_mode": "regional"}`, wantUnpriced: "regional NAT gateways"},
	})
}

func TestLoadBalancer(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{name: "application by default", typ: "aws_lb", values: `{}`, wantMonthly: "16.425", wantSummary: "Application Load Balancer", wantNotes: []string{"LCU) at $0.008/LCU-Hrs"}},
		{name: "aws_alb alias", typ: "aws_alb", values: `{"load_balancer_type": "application"}`, wantMonthly: "16.425", wantNotes: []string{"LCU"}},
		{name: "network", typ: "aws_lb", values: `{"load_balancer_type": "network"}`, wantMonthly: "16.425", wantSummary: "Network Load Balancer", wantNotes: []string{"$0.006"}},
		{name: "gateway", typ: "aws_lb", values: `{"load_balancer_type": "gateway"}`, wantMonthly: "9.125", wantSummary: "Gateway Load Balancer", wantNotes: []string{"$0.004"}},
		{name: "unknown type", typ: "aws_lb", values: `{"load_balancer_type": "classic"}`, wantUnpriced: `load_balancer_type "classic"`},
	})
}

func TestEKSCluster(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{
			name:        "standard support",
			typ:         "aws_eks_cluster",
			values:      `{"version": "1.33", "upgrade_policy": [{"support_type": "STANDARD"}]}`,
			wantMonthly: "73",
			wantSummary: "EKS cluster v1.33",
		},
		{
			name:        "extended support by default",
			typ:         "aws_eks_cluster",
			values:      `{"version": "1.33"}`,
			unknown:     `{"upgrade_policy": true}`,
			wantMonthly: "73",
			wantNotes:   []string{"extended support ($0.50/hr, $365.00/mo)"},
		},
		{
			name:        "auto mode",
			typ:         "aws_eks_cluster",
			values:      `{"upgrade_policy": [{"support_type": "STANDARD"}], "compute_config": [{"enabled": true}]}`,
			wantMonthly: "73",
			wantSummary: "EKS cluster",
			wantNotes:   []string{"Auto Mode"},
		},
	})
}

func TestEKSNodeGroup(t *testing.T) {
	runPricerTests(t, []pricerTest{
		{
			name:        "on-demand",
			typ:         "aws_eks_node_group",
			values:      `{"instance_types": ["m6i.large"], "scaling_config": [{"desired_size": 3}]}`,
			unknown:     `{"capacity_type": true, "ami_type": true}`,
			wantMonthly: "210.24",
			wantSummary: "3 × m6i.large",
			wantNotes:   []string{"root volumes not included"},
		},
		{
			name:        "several instance types",
			typ:         "aws_eks_node_group",
			values:      `{"capacity_type": "ON_DEMAND", "instance_types": ["m6i.large", "m5.large"], "scaling_config": [{"desired_size": 1}]}`,
			wantMonthly: "70.08",
			wantNotes:   []string{"root volumes", "priced as m6i.large; other instance types (m5.large)"},
		},
		{name: "scaled to zero", typ: "aws_eks_node_group", values: `{"instance_types": ["m6i.large"], "scaling_config": [{"desired_size": 0}]}`, wantMonthly: "0", wantNotes: []string{"root volumes"}},
		{name: "spot", typ: "aws_eks_node_group", values: `{"capacity_type": "SPOT", "instance_types": ["m6i.large"]}`, wantUnpriced: "spot"},
		{name: "windows", typ: "aws_eks_node_group", values: `{"ami_type": "WINDOWS_CORE_2022_x86_64", "instance_types": ["m6i.large"]}`, wantUnpriced: "Windows"},
		{name: "instance types from launch template", typ: "aws_eks_node_group", values: `{"instance_types": []}`, wantUnpriced: "instance_types is not set"},
		{name: "instance types unknown", typ: "aws_eks_node_group", values: `{}`, unknown: `{"instance_types": true}`, wantUnpriced: "instance_types is known only after apply"},
		{name: "desired size unknown", typ: "aws_eks_node_group", values: `{"instance_types": ["m6i.large"], "scaling_config": [{}]}`, unknown: `{"scaling_config": [{"desired_size": true}]}`, wantUnpriced: "desired_size is known only after apply"},
	})
}

func TestUSD(t *testing.T) {
	tests := map[string]string{"0.5": "$0.50", "0.5000000000": "$0.50", "0.0225": "$0.0225", "40.96": "$40.96", "365": "$365.00", "0.03185": "$0.03185"}
	for in, want := range tests {
		if got := usd(decimal.RequireFromString(in)); got != want {
			t.Errorf("usd(%s) = %s, want %s", in, got, want)
		}
	}
}
