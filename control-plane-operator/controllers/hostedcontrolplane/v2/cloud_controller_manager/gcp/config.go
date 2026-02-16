package gcp

import (
	"fmt"
	"strings"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	configKey = "cloud.conf"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	gcpPlatform := cpContext.HCP.Spec.Platform.GCP
	if gcpPlatform == nil {
		return fmt.Errorf("GCP platform configuration is nil")
	}

	projectID := gcpPlatform.Project
	networkName := gcpPlatform.NetworkConfig.Network.Name

	// Node tags are used for firewall rules. The nodepool controller applies
	// the tag "{infraID}-worker" to all worker nodes. GCP network tags must be
	// lowercase, so we apply the same transformation as the nodepool controller.
	nodeTags := strings.ToLower(fmt.Sprintf("%s-worker", cpContext.HCP.Spec.InfraID))

	// Get the config template and populate it
	configTemplate := cm.Data[configKey]
	config := fmt.Sprintf(configTemplate, projectID, networkName, nodeTags)

	cm.Data[configKey] = config
	return nil
}
