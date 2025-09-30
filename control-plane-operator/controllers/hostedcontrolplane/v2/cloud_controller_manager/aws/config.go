package aws

import (
	"fmt"
	"strconv"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	configKey                       = "aws.conf"
	defaultProbeMode                = "Shared"
	defaultKubeProxyHealthCheckPort = 10256
	defaultKubeProxyHealthCheckPath = "/healthz"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	clusterID := cpContext.HCP.Spec.InfraID
	config := cpContext.HCP.Spec.Platform.AWS.CloudProviderConfig
	probeMode := defaultProbeMode
	probePath := defaultKubeProxyHealthCheckPath
	probePort := strconv.Itoa(int(defaultKubeProxyHealthCheckPort))
	var zone, vpc, subnetID string
	if config != nil {
		zone = config.Zone
		vpc = config.VPC
		if id := config.Subnet.ID; id != nil {
			subnetID = *id
		}
		if config.ClusterServiceLoadBalancerHealthProbeMode != "" {
			probeMode = config.ClusterServiceLoadBalancerHealthProbeMode
		}
		if config.ClusterServiceSharedLoadBalancerHealthProbePath != "" {
			probePath = config.ClusterServiceSharedLoadBalancerHealthProbePath
		}
		if config.ClusterServiceSharedLoadBalancerHealthProbePort != 0 {
			probePort = strconv.Itoa(int(config.ClusterServiceSharedLoadBalancerHealthProbePort))
		}
	}

	configTemplate := cm.Data[configKey]
	cm.Data[configKey] = fmt.Sprintf(configTemplate, zone, vpc, clusterID, subnetID, probeMode, probePath, probePort)
	return nil
}
