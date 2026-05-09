package imageresolution

import (
	"fmt"
	"sort"
	"strings"
)

// ResolverConfig holds the two sources of registry overrides that feed the image resolver.
type ResolverConfig struct {
	// RegistryOverrides maps source registry prefixes to destination prefixes (CLI --registry-overrides).
	RegistryOverrides map[string]string
	// ImageRegistryMirrors maps source registry prefixes to ordered mirror lists (ICSP/IDMS).
	ImageRegistryMirrors map[string][]string
}

// RegistryOverridesFlag serializes RegistryOverrides into the comma-separated key=value format used by CLI flags.
func (c ResolverConfig) RegistryOverridesFlag() string {
	if len(c.RegistryOverrides) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.RegistryOverrides))
	for k := range c.RegistryOverrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, c.RegistryOverrides[k]))
	}
	return strings.Join(pairs, ",")
}

// ImageRegistryMirrorsEnvVar serializes ImageRegistryMirrors into the comma-separated key=value format used by environment variables.
func (c ResolverConfig) ImageRegistryMirrorsEnvVar() string {
	if len(c.ImageRegistryMirrors) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.ImageRegistryMirrors))
	for k := range c.ImageRegistryMirrors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		for _, v := range c.ImageRegistryMirrors[k] {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(pairs, ",")
}

// IsEmpty returns true when no overrides or mirrors are configured.
func (c ResolverConfig) IsEmpty() bool {
	return len(c.RegistryOverrides) == 0 && len(c.ImageRegistryMirrors) == 0
}

// Clone returns a deep copy of the config, including nested slice maps.
func (c ResolverConfig) Clone() ResolverConfig {
	return ResolverConfig{
		RegistryOverrides:    cloneStringMap(c.RegistryOverrides),
		ImageRegistryMirrors: cloneStringSliceMap(c.ImageRegistryMirrors),
	}
}

// ParseRegistryOverridesFlag parses a comma-separated "source=dest" flag string into a registry override map.
// Handles the legacy "=" sentinel value (produced by the old ConvertRegistryOverridesToCommandLineFlag
// for empty maps) as equivalent to empty.
func ParseRegistryOverridesFlag(flag string) (map[string]string, error) {
	if flag == "" || flag == "=" {
		return nil, nil
	}
	result := make(map[string]string)
	for pair := range strings.SplitSeq(flag, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid registry override %q", pair)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

// ParseImageRegistryMirrorsEnvVar parses a comma-separated "source=mirror" environment variable into a mirror map.
// Handles the legacy "=" sentinel value (produced by the old ConvertOpenShiftImageRegistryOverridesToCommandLineFlag
// for empty maps) as equivalent to empty.
func ParseImageRegistryMirrorsEnvVar(envVar string) (map[string][]string, error) {
	if envVar == "" || envVar == "=" {
		return nil, nil
	}
	result := make(map[string][]string)
	for pair := range strings.SplitSeq(envVar, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid image registry mirror %q", pair)
		}
		result[parts[0]] = append(result[parts[0]], parts[1])
	}
	return result, nil
}
