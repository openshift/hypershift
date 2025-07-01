package capabilities

import (
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
)

// IsNodeTuningCapabilityEnabled returns true if the NodeTuning capability is enabled, or false if disabled.
func IsNodeTuningCapabilityEnabled(capabilities *hyperv1.Capabilities) bool {
	if capabilities == nil {
		return true
	}
	for _, disabledCap := range capabilities.Disabled {
		if disabledCap == hyperv1.NodeTuningCapability {
			return false
		}
	}
	return true
}

// HasDisabledCapabilities returns true if any capabilities are disabled; otherwise, it returns false.
func HasDisabledCapabilities(capabilities *hyperv1.Capabilities) bool {
	if capabilities == nil {
		return false
	}
	return len(capabilities.Disabled) > 0
}

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

// CalculateEnabledCapabilities returns the net enabled capabilities, by
// using the default set of capabilities (minus baremetal capability) and the
// explicitly enabled and disabled capabilities, in alphabetical order.
func CalculateEnabledCapabilities(capabilities *hyperv1.Capabilities) []configv1.ClusterVersionCapability {
	vCurrent := configv1.ClusterVersionCapabilitySets[configv1.ClusterVersionCapabilitySetCurrent]
	netCaps := sets.New[configv1.ClusterVersionCapability](vCurrent...)
	netCaps.Delete(configv1.ClusterVersionCapabilityBaremetal)

	if capabilities != nil && len(capabilities.Disabled) > 0 {
		disabledCaps := make([]configv1.ClusterVersionCapability, len(capabilities.Disabled))
		for i, dc := range capabilities.Disabled {
			disabledCaps[i] = configv1.ClusterVersionCapability(dc)
		}
		netCaps = netCaps.Delete(disabledCaps...)
	}

	if capabilities != nil && len(capabilities.Enabled) > 0 {
		for _, ec := range capabilities.Enabled {
			if !netCaps.Has(configv1.ClusterVersionCapability(ec)) {
				netCaps.Insert(configv1.ClusterVersionCapability(ec))
			}
		}
	}

	return sortedCapabilities(netCaps.UnsortedList())
}

func sortedCapabilities(caps []configv1.ClusterVersionCapability) []configv1.ClusterVersionCapability {
	slices.SortFunc(caps, func(a, b configv1.ClusterVersionCapability) int {
		return strings.Compare(string(a), string(b))
	})
	return caps
}
