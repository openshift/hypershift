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

	ManagedEtcdSpec hyperv1.ManagedEtcdSpec
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
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetMultizoneSpread(etcdPodSelector)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.SetColocationAnchor(hcp)

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.DeploymentConfig.Replicas = 3
	default:
		// TODO: handle non-HA
		p.DeploymentConfig.Replicas = 3
	}

	// TODO: Figure out validation and defaulting sooner than this point
	if hcp.Spec.Etcd.Managed != nil {
		p.ManagedEtcdSpec = *hcp.Spec.Etcd.Managed
	} else {
		p.ManagedEtcdSpec = hyperv1.ManagedEtcdSpec{
			Storage: hyperv1.ManagedEtcdStorageSpec{
				Type: hyperv1.PersistentVolumeEtcdStorage,
				PersistentVolume: hyperv1.PersistentVolumeEtcdStorageSpec{
					StorageClassName: "gp2",
					Size:             resource.MustParse("4Gi"),
				},
			},
		}
	}

	return p
}
