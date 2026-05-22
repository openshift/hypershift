package etcdbackupgcs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	"k8s.io/apimachinery/pkg/api/meta"
)

const (
	ComponentName = "etcd-backup-gcs"
)

var _ component.ComponentOptions = &etcdBackupGCS{}

type etcdBackupGCS struct{}

func (e *etcdBackupGCS) IsRequestServing() bool {
	return false
}

func (e *etcdBackupGCS) MultiZoneSpread() bool {
	return false
}

func (e *etcdBackupGCS) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewCronJobComponent(ComponentName, &etcdBackupGCS{}).
		WithAdaptFunction(adaptCronJob).
		WithPredicate(predicate).
		WithManifestAdapter("serviceaccount.yaml",
			component.WithAdaptFunction(adaptServiceAccount),
		).
		WithManifestAdapter("role.yaml",
			component.WithAdaptFunction(adaptRole),
		).
		WithManifestAdapter("prometheus-alerting-rules.yaml",
			component.WithAdaptFunction(adaptAlertingRules),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	if cpContext.HCP.Spec.Etcd.Managed == nil ||
		cpContext.HCP.Spec.Etcd.Managed.AutomatedBackup == nil {
		return false, nil
	}
	if cpContext.HCP.Spec.Etcd.Managed.AutomatedBackup.Storage.Type != hyperv1.AutomatedEtcdBackupStorageTypeGCS {
		return false, nil
	}
	return meta.IsStatusConditionTrue(cpContext.HCP.Status.Conditions, string(hyperv1.EtcdAvailable)), nil
}
