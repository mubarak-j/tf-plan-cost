package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string) *Plan {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return p
}

func byAddress(p *Plan) map[string]ResourceChange {
	m := map[string]ResourceChange{}
	for _, rc := range p.Changes {
		m[rc.Address] = rc
	}
	return m
}

func TestParseFixtures(t *testing.T) {
	type want struct {
		action Action
		region string
	}
	tests := []struct {
		fixture string
		want    map[string]want
	}{
		{
			// Real `terraform show -json` output (Terraform 1.16, AWS provider v6).
			fixture: "create.json",
			want: map[string]want{
				"aws_db_instance.app":              {Create, "us-east-1"}, // provider region from var.region
				"aws_ebs_volume.data":              {Create, "us-east-1"},
				"aws_eks_cluster.main":             {Create, "us-east-1"},
				"aws_eks_node_group.default":       {Create, "us-east-1"},
				"aws_instance.api[0]":              {Create, "us-east-1"},
				"aws_instance.api[1]":              {Create, "us-east-1"},
				"aws_lb.web":                       {Create, "us-west-2"}, // aliased provider
				"aws_nat_gateway.main":             {Create, "us-east-1"},
				"aws_s3_bucket.logs":               {Create, "us-east-1"},
				"module.edge.aws_nat_gateway.edge": {Create, "us-west-2"}, // provider passed to module
			},
		},
		{
			fixture: "update.json",
			want: map[string]want{
				"aws_instance.api[0]":        {Update, "us-east-1"},
				"aws_ebs_volume.data":        {Update, "us-east-1"},
				"aws_eks_node_group.default": {Update, "us-east-1"},
				"aws_nat_gateway.main":       {NoOp, "us-east-1"},
				"aws_s3_bucket.logs":         {Update, "us-east-1"},
				"aws_iam_role.app":           {NoOp, "us-east-1"},
			},
		},
		{
			fixture: "replace.json",
			want: map[string]want{
				"aws_db_instance.app": {Replace, "us-east-1"}, // delete,create
				"aws_instance.web":    {Replace, "us-east-1"}, // create,delete (create_before_destroy)
				"aws_ebs_volume.fast": {Replace, "us-east-1"},
			},
		},
		{
			// Data sources are skipped; module instance keys are stripped to
			// find the module's provider configuration.
			fixture: "mixed.json",
			want: map[string]want{
				`module.app["blue"].aws_instance.api[0]`: {Create, "us-west-2"},
				"aws_ebs_volume.data":                    {Create, "us-east-1"},
				"aws_instance.spot":                      {Create, "us-east-1"},
				"aws_instance.dynamic":                   {Create, "us-east-1"}, // not in configuration: default provider
				"aws_db_instance.legacy":                 {Create, "us-east-1"},
				"aws_instance.bastion":                   {NoOp, "us-east-1"},
				"aws_instance.old":                       {Forget, "us-east-1"},
				"aws_sqs_queue.jobs":                     {Create, "us-east-1"},
			},
		},
		{
			// No provider configuration: region must come from resource attributes.
			fixture: "noop.json",
			want: map[string]want{
				"aws_nat_gateway.main": {NoOp, ""},
				"aws_s3_bucket.logs":   {NoOp, ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			p := parseFixture(t, tt.fixture)
			got := byAddress(p)
			if len(got) != len(tt.want) {
				t.Errorf("got %d changes, want %d", len(got), len(tt.want))
			}
			for addr, w := range tt.want {
				rc, ok := got[addr]
				if !ok {
					t.Errorf("missing %s", addr)
					continue
				}
				if rc.Action != w.action {
					t.Errorf("%s: action = %q, want %q", addr, rc.Action, w.action)
				}
				if rc.Region != w.region {
					t.Errorf("%s: region = %q, want %q", addr, rc.Region, w.region)
				}
			}
		})
	}
}

func TestParseValues(t *testing.T) {
	p := parseFixture(t, "update.json")
	rc := byAddress(p)["aws_instance.api[0]"]
	if rc.Before["instance_type"] != "m6i.large" || rc.After["instance_type"] != "m6i.xlarge" {
		t.Errorf("instance_type before/after = %v/%v", rc.Before["instance_type"], rc.After["instance_type"])
	}
	// Numbers are decoded as json.Number so prices stay exact.
	vol := byAddress(p)["aws_ebs_volume.data"]
	if n, ok := vol.After["size"].(json.Number); !ok || n.String() != "500" {
		t.Errorf("size = %#v, want json.Number(500)", vol.After["size"])
	}

	c := byAddress(parseFixture(t, "create.json"))["aws_instance.api[0]"]
	if c.Before != nil {
		t.Errorf("create: Before = %v, want nil", c.Before)
	}
	if c.AfterUnknown["id"] != true {
		t.Errorf("create: after_unknown.id = %v, want true", c.AfterUnknown["id"])
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, input, wantErr string
	}{
		{"invalid JSON", `{`, "decode plan JSON"},
		{"not a plan", `{"resource_changes": []}`, "missing format_version"},
		{"future format", `{"format_version": "2.0"}`, `unsupported plan format_version "2.0"`},
		{"unknown action", `{"format_version": "1.2", "resource_changes": [
			{"address": "aws_instance.x", "mode": "managed", "type": "aws_instance", "change": {"actions": ["teleport"]}}]}`,
			"aws_instance.x: unrecognized change actions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseEmptyPlan(t *testing.T) {
	p, err := Parse(strings.NewReader(`{"format_version": "1.0", "terraform_version": "1.5.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 0 || p.TerraformVersion != "1.5.0" {
		t.Errorf("got %+v", p)
	}
}

func TestConfigAddress(t *testing.T) {
	tests := map[string]string{
		"aws_instance.web":                                  "aws_instance.web",
		"aws_instance.web[0]":                               "aws_instance.web",
		`aws_instance.web["a.b"]`:                           "aws_instance.web",
		`module.a["x"].module.b[0].aws_instance.web[2]`:     "module.a.module.b.aws_instance.web",
		`module.a["we]ird\"key"].aws_lb.this`:               "module.a.aws_lb.this",
		`module.network.aws_nat_gateway.this["us-east-1a"]`: "module.network.aws_nat_gateway.this",
	}
	for in, want := range tests {
		if got := ConfigAddress(in); got != want {
			t.Errorf("ConfigAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
