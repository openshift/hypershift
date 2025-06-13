package capabilities

import (
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
)

// IsImageRegistryCapabilityEnabled returns true if the Image Registry
// capability is enabled, or false if disabled.
//
// The Image Registry capability is enabled by default.
func IsImageRegistryCapabilityEnabled(capabilities *hyperv1.Capabilities) bool {
	if capabilities == nil {
		return true
	}
	enabled := true
	for _, disabledCap := range capabilities.Disabled {
		if disabledCap == hyperv1.ImageRegistryCapability {
			enabled = false
		}
	}
	return enabled
}

// CalculateEnabledCapabilities returns the difference between the default set
// of enabled capabilities (vCurrent) and the given set of capabilities to
// disable, in alphabetical order.
func CalculateEnabledCapabilities(capabilities *hyperv1.Capabilities) []configv1.ClusterVersionCapability {
	vCurrent := configv1.ClusterVersionCapabilitySets[configv1.ClusterVersionCapabilitySetCurrent]
	enabledCaps := sets.New[configv1.ClusterVersionCapability](vCurrent...)

	if capabilities != nil && len(capabilities.Disabled) > 0 {
		disabledCaps := make([]configv1.ClusterVersionCapability, len(capabilities.Disabled))
		for i, dc := range capabilities.Disabled {
			disabledCaps[i] = configv1.ClusterVersionCapability(dc)
		}
		enabledCaps = enabledCaps.Delete(disabledCaps...)
	}

	return sortedCapabilities(enabledCaps.UnsortedList())
}

func sortedCapabilities(caps []configv1.ClusterVersionCapability) []configv1.ClusterVersionCapability {
	slices.SortFunc(caps, func(a, b configv1.ClusterVersionCapability) int {
		return strings.Compare(string(a), string(b))
	})
	return caps
}
