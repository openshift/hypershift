package etcd

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	"github.com/openshift/hypershift/support/config"
)

type EtcdParams struct {
	EtcdImage string
	CPOImage  string

	OwnerRef         config.OwnerRef `json:"ownerRef"`
	DeploymentConfig config.DeploymentConfig

	StorageSpec hyperv1.ManagedEtcdStorageSpec

	Availability hyperv1.AvailabilityPolicy

	SnapshotRestored bool
}

func etcdPodSelector() map[string]string {
	return map[string]string{"app": "etcd"}
}

func NewEtcdParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *EtcdParams {
	p := &EtcdParams{
		EtcdImage:    images["etcd"],
		CPOImage:     images["controlplane-operator"],
		OwnerRef:     config.OwnerRefFrom(hcp),
		Availability: hcp.Spec.ControllerAvailabilityPolicy,
	}
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		etcdContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("600Mi"),
				corev1.ResourceCPU:    resource.MustParse("300m"),
			},
		},
	}
	if p.DeploymentConfig.AdditionalLabels == nil {
		p.DeploymentConfig.AdditionalLabels = make(map[string]string)
	}
	p.DeploymentConfig.AdditionalLabels[hyperv1.ControlPlaneComponent] = "etcd"
	p.DeploymentConfig.Scheduling.PriorityClass = config.EtcdPriorityClass
	p.DeploymentConfig.SetDefaults(hcp, etcdPodSelector(), nil)

	if hcp.Spec.Etcd.Managed == nil {
		hcp.Spec.Etcd.Managed = &hyperv1.ManagedEtcdSpec{
			Storage: hyperv1.ManagedEtcdStorageSpec{
				Type: hyperv1.PersistentVolumeEtcdStorage,
			},
		}
	}
	switch hcp.Spec.Etcd.Managed.Storage.Type {
	case hyperv1.PersistentVolumeEtcdStorage:
		p.StorageSpec.PersistentVolume = &hyperv1.PersistentVolumeEtcdStorageSpec{
			StorageClassName: nil,
			Size:             &hyperv1.DefaultPersistentVolumeEtcdStorageSize,
		}
		if pv := hcp.Spec.Etcd.Managed.Storage.PersistentVolume; pv != nil {
			p.StorageSpec.PersistentVolume.StorageClassName = pv.StorageClassName
			if pv.Size != nil {
				p.StorageSpec.PersistentVolume.Size = pv.Size
			}
		}
	}

	if len(hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL) > 0 {
		p.StorageSpec.RestoreSnapshotURL = hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL
		p.SnapshotRestored = meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
	}

	return p
}
