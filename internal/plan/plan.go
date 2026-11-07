// Package plan parses the JSON output of `terraform show -json <planfile>`.
//
// Only the parts tf-plan-cost needs are decoded: managed resource changes,
// their before/after attribute values, which after-values are unknown until
// apply, and enough configuration to work out which AWS region each resource
// lives in.
package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Action is the normalized change Terraform plans for a resource.
type Action string

// Actions tf-plan-cost distinguishes. Replace covers both delete-then-create
// and create-before-destroy.
const (
	Create  Action = "create"
	Update  Action = "update"
	Delete  Action = "delete"
	Replace Action = "replace"
	NoOp    Action = "no-op"
	Forget  Action = "forget"
)

// ResourceChange is one managed resource instance from `resource_changes`.
type ResourceChange struct {
	Address      string // e.g. module.web.aws_instance.api[0]
	Type         string // e.g. aws_instance
	Name         string
	ProviderName string // e.g. registry.terraform.io/hashicorp/aws
	Action       Action

	// Before and After are the attribute values. Before is nil for creates,
	// After is nil for deletes. Numbers are json.Number to keep precision.
	Before map[string]any
	After  map[string]any
	// AfterUnknown mirrors After; a `true` leaf means "known after apply".
	AfterUnknown map[string]any

	// Region is the region of the provider configuration this resource uses,
	// resolved from the plan's configuration block. Empty if it could not be
	// determined statically (e.g. set via environment variables in CI).
	Region string
}

// Plan is a parsed Terraform plan.
type Plan struct {
	FormatVersion    string
	TerraformVersion string
	Changes          []ResourceChange
}

// Parse decodes `terraform show -json` output. It accepts plan format 1.x and
// returns only managed resources (data sources are skipped).
func Parse(r io.Reader) (*Plan, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw rawPlan
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode plan JSON: %w", err)
	}
	if raw.FormatVersion == "" {
		return nil, errors.New("not a Terraform plan: missing format_version (did you pass `terraform show -json` output?)")
	}
	if major, _, _ := strings.Cut(raw.FormatVersion, "."); major != "1" {
		return nil, fmt.Errorf("unsupported plan format_version %q (want 1.x)", raw.FormatVersion)
	}

	regions := newRegionResolver(&raw)

	p := &Plan{FormatVersion: raw.FormatVersion, TerraformVersion: raw.TerraformVersion}
	for _, rc := range raw.ResourceChanges {
		if rc.Mode != "managed" {
			continue
		}
		action, err := normalizeActions(rc.Change.Actions)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rc.Address, err)
		}
		p.Changes = append(p.Changes, ResourceChange{
			Address:      rc.Address,
			Type:         rc.Type,
			Name:         rc.Name,
			ProviderName: rc.ProviderName,
			Action:       action,
			Before:       asMap(rc.Change.Before),
			After:        asMap(rc.Change.After),
			AfterUnknown: asMap(rc.Change.AfterUnknown),
			Region:       regions.forAddress(rc.Address),
		})
	}
	return p, nil
}

func normalizeActions(actions []string) (Action, error) {
	switch strings.Join(actions, ",") {
	case "create":
		return Create, nil
	case "update":
		return Update, nil
	case "delete":
		return Delete, nil
	case "delete,create", "create,delete":
		return Replace, nil
	case "no-op", "read":
		return NoOp, nil
	case "forget":
		return Forget, nil
	default:
		return "", fmt.Errorf("unrecognized change actions %q", actions)
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

type rawPlan struct {
	FormatVersion    string                 `json:"format_version"`
	TerraformVersion string                 `json:"terraform_version"`
	Variables        map[string]rawVariable `json:"variables"`
	ResourceChanges  []rawResourceChange    `json:"resource_changes"`
	Configuration    rawConfiguration       `json:"configuration"`
}

type rawVariable struct {
	Value any `json:"value"`
}

type rawResourceChange struct {
	Address      string `json:"address"`
	Mode         string `json:"mode"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	ProviderName string `json:"provider_name"`
	Change       struct {
		Actions      []string `json:"actions"`
		Before       any      `json:"before"`
		After        any      `json:"after"`
		AfterUnknown any      `json:"after_unknown"`
	} `json:"change"`
}

type rawConfiguration struct {
	ProviderConfig map[string]rawProviderConfig `json:"provider_config"`
	RootModule     rawModule                    `json:"root_module"`
}

type rawProviderConfig struct {
	Name        string                   `json:"name"`
	Expressions map[string]rawExpression `json:"expressions"`
}

type rawExpression struct {
	ConstantValue any      `json:"constant_value"`
	References    []string `json:"references"`
}

type rawModule struct {
	Resources []struct {
		Address           string `json:"address"`
		ProviderConfigKey string `json:"provider_config_key"`
	} `json:"resources"`
	ModuleCalls map[string]struct {
		Module rawModule `json:"module"`
	} `json:"module_calls"`
}
