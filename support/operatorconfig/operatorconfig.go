// Package operatorconfig derives, and compares, the set of settings configured under
// HostedCluster.spec.operatorConfiguration.
//
// The control plane operator only deserializes the settings its own build knows about.
// During a HyperShift-operator / control-plane-operator version skew window the API
// server accepts and persists a setting added in a later release, mirrors it onto the
// HostedControlPlane, and the older control plane operator drops it on decode and
// silently ignores it.
//
// The helpers here turn that decode behavior into a signal. Both operators run
// FieldPaths over the same HostedControlPlane spec bytes, each with its own compiled-in
// types, so the control plane operator's result is exactly the subset of settings it
// understands. The difference between the two results names the settings that are not
// in effect, with neither side needing to know anything about the individual fields.
package operatorconfig

import (
	"encoding/json"
	"fmt"
	"slices"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/blang/semver"
)

// MaxHonoredFields bounds how many paths are reported on
// HostedControlPlane.status.operatorConfiguration.honoredFields. It must match the
// MaxItems marker on that field. A configuration that exceeds it is not reported at
// all, rather than reported truncated, because a truncated list is indistinguishable
// from settings being dropped and would be read as a false skew report.
const MaxHonoredFields = 200

// honoredFieldsReportingMinVersion is the first control plane operator version that
// populates HostedControlPlane.status.operatorConfiguration.honoredFields. Older
// versions report nothing, so what they honor can only be established from
// preReportingFieldFloors.
var honoredFieldsReportingMinVersion = semver.MustParse("5.1.0")

// preReportingFieldFloors maps a setting to the first control plane operator version
// that honors it, for settings that shipped before honoredFields reporting existed and
// so cannot be covered by it.
//
// This map is closed. Settings introduced from honoredFieldsReportingMinVersion onwards
// are reported generically and must not be added here; every entry can be deleted once
// honoredFieldsReportingMinVersion drops out of the supported skew window.
var preReportingFieldFloors = map[string]semver.Version{
	// Added by CNTRLPLANE-4417 in the same release that introduced honoredFields
	// reporting, so no control plane operator that honors it fails to report it.
	"kubeAPIServer.eventTTLMinutes": honoredFieldsReportingMinVersion,
}

// FieldPaths returns the dot-separated paths of every setting set in cfg, relative to
// operatorConfiguration itself and sorted lexically, for example
// "kubeAPIServer.eventTTLMinutes". It returns nil when nothing is set.
//
// Paths are derived from the JSON encoding of cfg rather than by reflection, so the
// omitempty and omitzero tags on the API types decide what counts as "set", exactly as
// they do on the wire. Elements of a list of objects are indexed, for example
// "foo[0].bar"; a list of scalars is reported as the single path of the list itself.
func FieldPaths(cfg *hyperv1.OperatorConfiguration) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal operator configuration: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("failed to unmarshal operator configuration: %w", err)
	}

	var paths []string
	collectFieldPaths(decoded, "", &paths)
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func collectFieldPaths(value any, prefix string, paths *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			break
		}
		for key, child := range typed {
			childPrefix := key
			if prefix != "" {
				childPrefix = prefix + "." + key
			}
			collectFieldPaths(child, childPrefix, paths)
		}
		return
	case []any:
		// Only descend into lists of objects: a list of scalars has no nested settings
		// to drop, and indexing it would add noise without adding signal.
		containsObject := false
		for _, element := range typed {
			if _, isObject := element.(map[string]any); isObject {
				containsObject = true
				break
			}
		}
		if !containsObject {
			break
		}
		for i, element := range typed {
			collectFieldPaths(element, fmt.Sprintf("%s[%d]", prefix, i), paths)
		}
		return
	}

	// A scalar, an empty object, or a list with no nested settings: the path itself is
	// the setting. The empty prefix means cfg encoded to something with no fields at
	// all, which is not a setting.
	if prefix != "" {
		*paths = append(*paths, prefix)
	}
}

// Reportable reports whether paths fit within the bound on
// HostedControlPlane.status.operatorConfiguration.honoredFields. The current API cannot
// express a configuration this large; the check exists so that growing the API can never
// turn into a status write the API server rejects, taking the rest of the status with it.
func Reportable(paths []string) bool {
	return len(paths) <= MaxHonoredFields
}

// Unhonored returns the paths in configured that are absent from honored, preserving
// the order of configured. Paths in honored that are not configured are ignored: an
// older control plane operator serializing a setting the current build no longer emits
// says nothing about whether what the user asked for is in effect.
func Unhonored(configured, honored []string) []string {
	var unhonored []string
	for _, path := range configured {
		if !slices.Contains(honored, path) {
			unhonored = append(unhonored, path)
		}
	}
	return unhonored
}

// SupportsHonoredFieldsReporting reports whether a control plane operator at the given
// version populates HostedControlPlane.status.operatorConfiguration.honoredFields.
// known is false for an empty or malformed version, which must not be read as support.
// Patch, prerelease, and build metadata do not affect support.
func SupportsHonoredFieldsReporting(version string) (supported bool, known bool) {
	parsed, err := semver.Parse(version)
	if err != nil {
		return false, false
	}
	return meetsFloor(parsed, honoredFieldsReportingMinVersion), true
}

// UnhonoredBefore returns the configured paths that a control plane operator at the
// given version is known not to honor, for settings that predate honoredFields
// reporting. It returns nothing for an empty or malformed version, and nothing for a
// setting with no recorded floor, since absence of a floor is not evidence of support.
func UnhonoredBefore(configured []string, version string) []string {
	parsed, err := semver.Parse(version)
	if err != nil {
		return nil
	}
	var unhonored []string
	for _, path := range configured {
		floor, hasFloor := preReportingFieldFloors[path]
		if hasFloor && !meetsFloor(parsed, floor) {
			unhonored = append(unhonored, path)
		}
	}
	return unhonored
}

// meetsFloor compares major and minor only, so that a prerelease or a z-stream of a
// supporting minor counts as supporting. semver orders 5.1.0-rc.1 before 5.1.0, but a
// 5.1 nightly carries the 5.1 behavior.
func meetsFloor(version, floor semver.Version) bool {
	if version.Major != floor.Major {
		return version.Major > floor.Major
	}
	return version.Minor >= floor.Minor
}
