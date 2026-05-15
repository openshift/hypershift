package etcdbackupgcs

import (
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

func adaptServiceAccount(cpContext component.WorkloadContext, sa *corev1.ServiceAccount) error {
	if cpContext.HCP.Spec.Etcd.Managed == nil || cpContext.HCP.Spec.Etcd.Managed.AutomatedBackup == nil {
		return nil
	}
	backupConfig := cpContext.HCP.Spec.Etcd.Managed.AutomatedBackup
	if sa.Annotations == nil {
		sa.Annotations = make(map[string]string)
	}
	sa.Annotations["iam.gke.io/gcp-service-account"] = backupConfig.Storage.GCS.GCPServiceAccount
	return nil
}
