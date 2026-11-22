package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Strip reads `terraform show -json` output and writes a reduced plan that
// holds only what Parse needs to price it:
//
//   - managed resource changes, with every value marked in before_sensitive
//     or after_sensitive replaced by null;
//   - the `region` expression of each AWS provider configuration;
//   - the root variables those region expressions reference (unless the
//     variable is declared sensitive);
//   - the resource-to-provider mapping of the configuration.
//
// Everything else (prior state, planned values, outputs, other provider
// arguments such as access keys, unrelated variables) is dropped, so the
// result can be sent to the tf-plan-cost service without the plan's secrets.
// Parse(Strip(p)) prices exactly like Parse(p).
func Strip(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var in stripInput
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("decode plan JSON: %w", err)
	}
	if in.FormatVersion == "" {
		return errors.New("not a Terraform plan: missing format_version (did you pass `terraform show -json` output?)")
	}
	if major, _, _ := strings.Cut(in.FormatVersion, "."); major != "1" {
		return fmt.Errorf("unsupported plan format_version %q (want 1.x)", in.FormatVersion)
	}

	out := strippedPlan{
		FormatVersion:    in.FormatVersion,
		TerraformVersion: in.TerraformVersion,
		Variables:        map[string]rawVariable{},
		ResourceChanges:  []strippedChange{},
		Configuration: strippedConfiguration{
			ProviderConfig: map[string]strippedProviderConfig{},
			RootModule:     stripModule(in.Configuration.RootModule),
		},
	}

	for _, rc := range in.ResourceChanges {
		if rc.Mode != "managed" {
			continue
		}
		c := rc.Change
		out.ResourceChanges = append(out.ResourceChanges, strippedChange{
			Address:      rc.Address,
			Mode:         rc.Mode,
			Type:         rc.Type,
			Name:         rc.Name,
			ProviderName: rc.ProviderName,
			Change: strippedChangeBody{
				Actions:      c.Actions,
				Before:       redact(c.Before, c.BeforeSensitive),
				After:        redact(c.After, c.AfterSensitive),
				AfterUnknown: c.AfterUnknown,
			},
		})
	}

	for key, pc := range in.Configuration.ProviderConfig {
		if pc.Name != "aws" {
			continue
		}
		spc := strippedProviderConfig{Name: pc.Name, Expressions: map[string]rawExpression{}}
		if expr, ok := pc.Expressions["region"]; ok {
			spc.Expressions["region"] = expr
			// Only root provider configurations can use root variables.
			if !strings.Contains(key, ":") {
				for _, ref := range expr.References {
					name, ok := strings.CutPrefix(ref, "var.")
					if !ok || strings.Contains(name, ".") {
						continue
					}
					if in.Configuration.RootModule.Variables[name].Sensitive {
						continue
					}
					if v, ok := in.Variables[name]; ok {
						out.Variables[name] = v
					}
				}
			}
		}
		out.Configuration.ProviderConfig[key] = spc
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

// redact returns v with every leaf marked true in the sensitivity mask
// replaced by nil. The mask mirrors v: `true` marks the whole value, objects
// and lists mark their elements. The input is not modified.
func redact(v, mask any) any {
	switch m := mask.(type) {
	case bool:
		if m {
			return nil
		}
		return v
	case map[string]any:
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		out := make(map[string]any, len(obj))
		for k, val := range obj {
			out[k] = redact(val, m[k])
		}
		return out
	case []any:
		list, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, len(list))
		for i, val := range list {
			var em any
			if i < len(m) {
				em = m[i]
			}
			out[i] = redact(val, em)
		}
		return out
	default:
		return v
	}
}

func stripModule(m stripInputModule) strippedModule {
	out := strippedModule{Resources: []strippedResource{}}
	for _, r := range m.Resources {
		out.Resources = append(out.Resources, strippedResource(r))
	}
	if len(m.ModuleCalls) > 0 {
		out.ModuleCalls = map[string]strippedModuleCall{}
		for name, call := range m.ModuleCalls {
			out.ModuleCalls[name] = strippedModuleCall{Module: stripModule(call.Module)}
		}
	}
	return out
}

// Input shapes: the parts of the plan Strip reads, including the sensitivity
// masks and variable declarations that Parse itself ignores.

type stripInput struct {
	FormatVersion    string                 `json:"format_version"`
	TerraformVersion string                 `json:"terraform_version"`
	Variables        map[string]rawVariable `json:"variables"`
	ResourceChanges  []struct {
		Address      string `json:"address"`
		Mode         string `json:"mode"`
		Type         string `json:"type"`
		Name         string `json:"name"`
		ProviderName string `json:"provider_name"`
		Change       struct {
			Actions         []string `json:"actions"`
			Before          any      `json:"before"`
			After           any      `json:"after"`
			AfterUnknown    any      `json:"after_unknown"`
			BeforeSensitive any      `json:"before_sensitive"`
			AfterSensitive  any      `json:"after_sensitive"`
		} `json:"change"`
	} `json:"resource_changes"`
	Configuration struct {
		ProviderConfig map[string]rawProviderConfig `json:"provider_config"`
		RootModule     stripInputModule             `json:"root_module"`
	} `json:"configuration"`
}

type stripInputModule struct {
	Resources []struct {
		Address           string `json:"address"`
		ProviderConfigKey string `json:"provider_config_key"`
	} `json:"resources"`
	ModuleCalls map[string]struct {
		Module stripInputModule `json:"module"`
	} `json:"module_calls"`
	Variables map[string]struct {
		Sensitive bool `json:"sensitive"`
	} `json:"variables"`
}

// Output shapes: a valid (reduced) plan format 1.x document.

type strippedPlan struct {
	FormatVersion    string                 `json:"format_version"`
	TerraformVersion string                 `json:"terraform_version,omitempty"`
	Variables        map[string]rawVariable `json:"variables"`
	ResourceChanges  []strippedChange       `json:"resource_changes"`
	Configuration    strippedConfiguration  `json:"configuration"`
}

type strippedChange struct {
	Address      string             `json:"address"`
	Mode         string             `json:"mode"`
	Type         string             `json:"type"`
	Name         string             `json:"name"`
	ProviderName string             `json:"provider_name"`
	Change       strippedChangeBody `json:"change"`
}

type strippedChangeBody struct {
	Actions      []string `json:"actions"`
	Before       any      `json:"before"`
	After        any      `json:"after"`
	AfterUnknown any      `json:"after_unknown"`
}

type strippedConfiguration struct {
	ProviderConfig map[string]strippedProviderConfig `json:"provider_config"`
	RootModule     strippedModule                    `json:"root_module"`
}

type strippedProviderConfig struct {
	Name        string                   `json:"name"`
	Expressions map[string]rawExpression `json:"expressions"`
}

type strippedModule struct {
	Resources   []strippedResource            `json:"resources"`
	ModuleCalls map[string]strippedModuleCall `json:"module_calls,omitempty"`
}

type strippedResource struct {
	Address           string `json:"address"`
	ProviderConfigKey string `json:"provider_config_key"`
}

type strippedModuleCall struct {
	Module strippedModule `json:"module"`
}
