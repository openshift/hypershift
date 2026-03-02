package aws

import (
	"fmt"
	"strconv"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	configKey                                  = "aws.conf"
	loadBalancerHealthProbeModeShared          = "Shared"
	loadBalancerHealthProbeModeServiceNodePort = "ServiceNodePort"
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
	if featuregates.Gate().Enabled(featuregates.AWSServiceLBNetworkSecurityGroup) {
		baseConfig += "\nNLBSecurityGroupMode = Managed"
	}

	cm.Data[configKey] = baseConfig
	return nil
}
