package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func strip(t *testing.T, in string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := Strip(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Strip: %v", err)
	}
	return out.Bytes()
}

// TestStripParsesTheSame checks that every fixture parses to the same Plan
// before and after stripping (the fixtures carry no sensitive values that
// pricing reads).
func TestStripParsesTheSame(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "plans", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := Parse(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			stripped := strip(t, string(data))
			got, err := Parse(bytes.NewReader(stripped))
			if err != nil {
				t.Fatalf("Parse(stripped): %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("stripped plan parses differently\n got: %+v\nwant: %+v", got, want)
			}
			if len(stripped) >= len(data) {
				t.Errorf("stripped plan is not smaller: %d >= %d bytes", len(stripped), len(data))
			}
		})
	}
}

const sensitivePlan = `{
  "format_version": "1.2",
  "terraform_version": "1.16.0",
  "variables": {
    "region":      {"value": "eu-west-1"},
    "db_password": {"value": "hunter2"},
    "secret_region": {"value": "ap-south-1"}
  },
  "planned_values": {"root_module": {"resources": [{"values": {"password": "hunter2"}}]}},
  "prior_state": {"values": {"password": "hunter2"}},
  "output_changes": {"pw": {"after": "hunter2"}},
  "resource_changes": [
    {
      "address": "aws_db_instance.app", "mode": "managed", "type": "aws_db_instance",
      "name": "app", "provider_name": "registry.terraform.io/hashicorp/aws",
      "change": {
        "actions": ["update"],
        "before": {"instance_class": "db.t3.micro", "password": "old-secret",
                   "tags": {"owner": "a", "token": "tag-secret"},
                   "blocks": [{"key": "list-secret", "size": 10}]},
        "after":  {"instance_class": "db.t3.small", "password": "hunter2",
                   "tags": {"owner": "a", "token": "tag-secret"},
                   "blocks": [{"key": "list-secret", "size": 20}]},
        "after_unknown": {},
        "before_sensitive": {"password": true, "tags": {"token": true}, "blocks": [{"key": true}]},
        "after_sensitive":  {"password": true, "tags": {"token": true}, "blocks": [{"key": true}]}
      }
    },
    {
      "address": "data.aws_ami.x", "mode": "data", "type": "aws_ami", "name": "x",
      "provider_name": "registry.terraform.io/hashicorp/aws",
      "change": {"actions": ["read"], "before": null, "after": {"owner": "data-secret"}, "after_unknown": {}}
    },
    {
      "address": "aws_secretsmanager_secret_version.s", "mode": "managed",
      "type": "aws_secretsmanager_secret_version", "name": "s",
      "provider_name": "registry.terraform.io/hashicorp/aws",
      "change": {"actions": ["create"], "before": null, "after": "whole-secret",
                 "after_unknown": {}, "after_sensitive": true}
    }
  ],
  "configuration": {
    "provider_config": {
      "aws": {"name": "aws", "expressions": {
        "region": {"references": ["var.region"]},
        "access_key": {"constant_value": "AKIA-secret"}
      }},
      "aws.other": {"name": "aws", "alias": "other", "expressions": {
        "region": {"references": ["var.secret_region"]}
      }},
      "google": {"name": "google", "expressions": {"credentials": {"constant_value": "gcp-secret"}}}
    },
    "root_module": {
      "variables": {"region": {}, "db_password": {"sensitive": true}, "secret_region": {"sensitive": true}},
      "resources": [
        {"address": "aws_db_instance.app", "provider_config_key": "aws"},
        {"address": "aws_secretsmanager_secret_version.s", "provider_config_key": "aws"}
      ]
    }
  }
}`

func TestStripRemovesSecrets(t *testing.T) {
	out := string(strip(t, sensitivePlan))
	for _, secret := range []string{
		"hunter2", "old-secret", "tag-secret", "list-secret", "whole-secret",
		"data-secret", "AKIA-secret", "gcp-secret", "ap-south-1", "db_password",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("stripped plan still contains %q:\n%s", secret, out)
		}
	}

	var got struct {
		Variables       map[string]any `json:"variables"`
		ResourceChanges []struct {
			Change struct {
				After any `json:"after"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"region": map[string]any{"value": "eu-west-1"}}; !reflect.DeepEqual(got.Variables, want) {
		t.Errorf("variables = %v, want %v", got.Variables, want)
	}
	if len(got.ResourceChanges) != 2 {
		t.Fatalf("got %d resource changes, want 2 (data source dropped)", len(got.ResourceChanges))
	}
	wantAfter := map[string]any{
		"instance_class": "db.t3.small", "password": nil,
		"tags":   map[string]any{"owner": "a", "token": nil},
		"blocks": []any{map[string]any{"key": nil, "size": 20.0}},
	}
	if !reflect.DeepEqual(got.ResourceChanges[0].Change.After, wantAfter) {
		t.Errorf("after = %v, want %v", got.ResourceChanges[0].Change.After, wantAfter)
	}
	if got.ResourceChanges[1].Change.After != nil {
		t.Errorf("fully sensitive value = %v, want null", got.ResourceChanges[1].Change.After)
	}

	// The stripped plan still resolves regions and prices the same fields.
	p, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	rc := p.Changes[0]
	if rc.Region != "eu-west-1" || rc.After["instance_class"] != "db.t3.small" || rc.Before["instance_class"] != "db.t3.micro" {
		t.Errorf("parsed stripped change = %+v", rc)
	}
}

func TestStripDoesNotModifyInput(t *testing.T) {
	v := map[string]any{"a": "secret", "b": []any{"x"}}
	_ = redact(v, map[string]any{"a": true, "b": []any{true}})
	if v["a"] != "secret" || v["b"].([]any)[0] != "x" {
		t.Errorf("redact modified its input: %v", v)
	}
}

func TestStripErrors(t *testing.T) {
	for name, in := range map[string]string{
		"not json":       "nope",
		"not a plan":     `{"foo": 1}`,
		"format version": `{"format_version": "2.0"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := Strip(strings.NewReader(in), &bytes.Buffer{}); err == nil {
				t.Error("Strip succeeded, want error")
			}
		})
	}
}
