package plan

import "strings"

// regionResolver maps resource config addresses to the static region of the
// AWS provider configuration they use.
type regionResolver struct {
	providerKeyByAddr map[string]string // config address -> provider_config_key
	regionByKey       map[string]string // provider_config_key -> region ("" if unknown)
}

func newRegionResolver(raw *rawPlan) *regionResolver {
	r := &regionResolver{
		providerKeyByAddr: map[string]string{},
		regionByKey:       map[string]string{},
	}
	r.walk("", raw.Configuration.RootModule)
	for key, pc := range raw.Configuration.ProviderConfig {
		if pc.Name != "aws" {
			continue
		}
		r.regionByKey[key] = providerRegion(key, pc, raw.Variables)
	}
	return r
}

func (r *regionResolver) walk(prefix string, m rawModule) {
	for _, res := range m.Resources {
		addr := res.Address
		if prefix != "" && !strings.HasPrefix(addr, "module.") {
			addr = prefix + addr
		}
		r.providerKeyByAddr[addr] = res.ProviderConfigKey
	}
	for name, call := range m.ModuleCalls {
		r.walk(prefix+"module."+name+".", call.Module)
	}
}

// forAddress returns the region for a resource instance address, or "".
func (r *regionResolver) forAddress(addr string) string {
	key, ok := r.providerKeyByAddr[ConfigAddress(addr)]
	if ok {
		if region, ok := r.regionByKey[key]; ok {
			return region
		}
	}
	// Fall back to the default root provider configuration.
	return r.regionByKey["aws"]
}

// providerRegion resolves the `region` argument of a provider block when it is
// a literal or a direct reference to a root module variable.
func providerRegion(key string, pc rawProviderConfig, vars map[string]rawVariable) string {
	expr, ok := pc.Expressions["region"]
	if !ok {
		return ""
	}
	if s, ok := expr.ConstantValue.(string); ok {
		return s
	}
	// Variables are only available for the root module.
	if strings.Contains(key, ":") {
		return ""
	}
	for _, ref := range expr.References {
		name, ok := strings.CutPrefix(ref, "var.")
		if !ok || strings.Contains(name, ".") {
			continue
		}
		if s, ok := vars[name].Value.(string); ok {
			return s
		}
	}
	return ""
}

// ConfigAddress strips instance keys from a resource instance address:
// `module.a["x"].module.b[0].aws_instance.web[2]` -> `module.a.module.b.aws_instance.web`.
func ConfigAddress(addr string) string {
	var b strings.Builder
	depth := 0
	inString := false
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		switch {
		case inString:
			switch c {
			case '\\':
				i++ // skip escaped char
			case '"':
				inString = false
			}
		case c == '"' && depth > 0:
			inString = true
		case c == '[':
			depth++
		case c == ']':
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return b.String()
}
