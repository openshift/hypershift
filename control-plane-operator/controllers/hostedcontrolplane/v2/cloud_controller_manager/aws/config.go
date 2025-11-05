package aws

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	configKey = "aws.conf"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	clusterID := cpContext.HCP.Spec.InfraID
	config := cpContext.HCP.Spec.Platform.AWS.CloudProviderConfig
	var zone, vpc, subnetID string
	if config != nil {
		zone = config.Zone
		vpc = config.VPC
		if config.Subnet != nil {
			subnetID = ptr.Deref(config.Subnet.ID, "")
		}
	}

	configTemplate := cm.Data[configKey]
	cm.Data[configKey] = fmt.Sprintf(configTemplate, zone, vpc, clusterID, subnetID)
	return nil
}
