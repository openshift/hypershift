package etcd

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type EtcdParams struct {
	EtcdOperatorImage string

	OwnerRef         config.OwnerRef `json:"ownerRef"`
	DeploymentConfig config.DeploymentConfig

	SnapshotInterval time.Duration
	SnapshotTTL      time.Duration

	StorageSpec hyperv1.ManagedEtcdStorageSpec
}

var etcdPodSelector = map[string]string{
	"app": "etcd",
}

func NewEtcdParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *EtcdParams {
	p := &EtcdParams{
		EtcdOperatorImage: images["etcd-operator"],
		OwnerRef:          config.OwnerRefFrom(hcp),
		SnapshotInterval:  30 * time.Minute,
		SnapshotTTL:       2 * time.Hour,
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
	p.DeploymentConfig.SetMultizoneSpread(etcdPodSelector)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.SetColocationAnchor(hcp)

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.DeploymentConfig.Replicas = 3
	default:
		p.DeploymentConfig.Replicas = 1
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

	return p
}
