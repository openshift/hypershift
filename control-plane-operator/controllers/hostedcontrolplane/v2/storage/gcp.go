package storage

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const gcpPDCloudConfigKey = "cloud.conf"

// adaptGCPPDCSIConfig populates the gcp-pd-cloud-config ConfigMap consumed by the
// GCP PD CSI driver controller's --cloud-config flag (see csi-operator's
// controller_add_hypershift_controller_minter.yaml patch, HyperShift-only).
//
// Without this file, the driver's getProjectAndZone() falls back to the GCE
// instance metadata server for the project ID. On HyperShift the controller
// pod runs on a management-cluster node, so that lookup resolves to the
// management cluster's own GCP project rather than the tenant's, even though
// WIF is correctly impersonating a tenant-project service account.
func adaptGCPPDCSIConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	gcpPlatform := cpContext.HCP.Spec.Platform.GCP
	if gcpPlatform == nil {
		return fmt.Errorf("GCP platform configuration is nil")
	}

	cm.Data[gcpPDCloudConfigKey] = fmt.Sprintf("[Global]\nproject-id = %s\n", gcpPlatform.Project)
	return nil
}
