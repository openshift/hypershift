package aws

import (
	"fmt"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	configKey                                  = "aws.conf"
	loadBalancerHealthProbeModeShared          = "Shared"
	loadBalancerHealthProbeModeServiceNodePort = "ServiceNodePort"

	nlbSecurityGroupModeConfig  = "NLBSecurityGroupMode"
	nlbSecurityGroupModeManaged = "Managed"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	clusterID := cpContext.HCP.Spec.InfraID
	config := cpContext.HCP.Spec.Platform.AWS.CloudProviderConfig
	probeMode := loadBalancerHealthProbeModeShared
	var zone, vpc, subnetID string
	if config != nil {
		zone = config.Zone
		vpc = config.VPC
		if config.Subnet != nil {
			subnetID = ptr.Deref(config.Subnet.ID, "")
		}
	}

	// Check for annotation overrides
	if mode, ok := cpContext.HCP.Annotations[hyperv1.AWSLoadBalancerHealthProbeModeAnnotation]; ok {
		if mode == loadBalancerHealthProbeModeShared || mode == loadBalancerHealthProbeModeServiceNodePort {
			probeMode = mode
		} else {
			return fmt.Errorf("invalid value for annotation %s: %s (valid values: %s, %s)", hyperv1.AWSLoadBalancerHealthProbeModeAnnotation, mode, loadBalancerHealthProbeModeShared, loadBalancerHealthProbeModeServiceNodePort)
		}
	}

	// Start with base config from template (only zone, vpc, clusterID, subnetID, probeMode)
	configTemplate := cm.Data[configKey]
	baseConfig := fmt.Sprintf(configTemplate, zone, vpc, clusterID, subnetID, probeMode)

	// Only add probe path if annotation is present
	if path, ok := cpContext.HCP.Annotations[hyperv1.SharedLoadBalancerHealthProbePathAnnotation]; ok && probeMode == loadBalancerHealthProbeModeShared {
		baseConfig += fmt.Sprintf("\nClusterServiceSharedLoadBalancerHealthProbePath = %s", path)
	}

	// Only add probe port if annotation is present
	if portStr, ok := cpContext.HCP.Annotations[hyperv1.SharedLoadBalancerHealthProbePortAnnotation]; ok && probeMode == loadBalancerHealthProbeModeShared {
		portNum, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("invalid value for annotation %s: %s (must be a valid port number)", hyperv1.SharedLoadBalancerHealthProbePortAnnotation, portStr)
		}
		if portNum < 1 || portNum > 65535 {
			return fmt.Errorf("invalid value for annotation %s: %d (must be between 1 and 65535)", hyperv1.SharedLoadBalancerHealthProbePortAnnotation, portNum)
		}
		baseConfig += fmt.Sprintf("\nClusterServiceSharedLoadBalancerHealthProbePort = %s", portStr)
	}

	// Add NLBSecurityGroupMode when the AWSServiceLBNetworkSecurityGroup feature gate is enabled for this cluster.
	if err := setNlbSecurityGroupMode(cpContext, &baseConfig); err != nil {
		return fmt.Errorf("failed to set NLBSecurityGroupMode: %w", err)
	}

	cm.Data[configKey] = baseConfig
	return nil
}

// setNlbSecurityGroupMode sets the NLBSecurityGroupMode in the baseConfig if the AWSServiceLBNetworkSecurityGroup feature gate is enabled for this cluster.
func setNlbSecurityGroupMode(cpContext component.WorkloadContext, baseConfig *string) error {
	if baseConfig == nil {
		return fmt.Errorf("baseConfig is required")
	}

	// Only apply to AWS platform - NLB security groups are AWS-specific
	if cpContext.HCP.Spec.Platform.Type != hyperv1.AWSPlatform {
		return nil
	}

	notInConfig := true
	if strings.Contains(*baseConfig, nlbSecurityGroupModeConfig) {
		notInConfig = false
	}

	// If no feature gate configuration, feature is disabled
	if cpContext.HCP.Spec.Configuration == nil || cpContext.HCP.Spec.Configuration.FeatureGate == nil {
		return nil
	}

	// Check if feature gate is enabled
	enabled, err := isFeatureGateEnabled(cpContext.HCP.Spec.Configuration.FeatureGate)
	if err != nil {
		return fmt.Errorf("failed to check feature gate: %w", err)
	}

	if enabled && notInConfig {
		// Only add if not already set
		*baseConfig += fmt.Sprintf("\n%s = %s", nlbSecurityGroupModeConfig, nlbSecurityGroupModeManaged)
	}

	return nil
}

// isFeatureGateEnabled checks if the AWSServiceLBNetworkSecurityGroup feature gate is enabled
// for the given feature gate spec. It handles both fixed feature sets and CustomNoUpgrade.
func isFeatureGateEnabled(spec *configv1.FeatureGateSpec) (bool, error) {
	if spec == nil {
		return false, nil
	}

	// CustomNoUpgrade requires checking explicit enabled/disabled lists
	if spec.FeatureSet == configv1.CustomNoUpgrade {
		if spec.CustomNoUpgrade == nil {
			return false, nil
		}

		// Check if explicitly disabled
		for _, disabled := range spec.CustomNoUpgrade.Disabled {
			if disabled == "AWSServiceLBNetworkSecurityGroup" {
				return false, nil
			}
		}

		// Check if explicitly enabled
		for _, enabled := range spec.CustomNoUpgrade.Enabled {
			if enabled == "AWSServiceLBNetworkSecurityGroup" {
				return true, nil
			}
		}

		// Not in enabled or disabled list - default to false
		return false, nil
	}

	// For fixed feature sets (Default, TechPreviewNoUpgrade, etc), use the feature gate system
	gates, err := featuregates.AllFeatures().FeatureGatesForFeatureSet(spec.FeatureSet)
	if err != nil {
		return false, fmt.Errorf("failed to get feature gates for feature set %s: %w", spec.FeatureSet, err)
	}

	return gates.Enabled(featuregates.AWSServiceLBNetworkSecurityGroup), nil
}
