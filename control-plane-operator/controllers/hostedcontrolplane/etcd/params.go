package etcd

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type EtcdParams struct {
	ClusterVersion            string
	EtcdOperatorImage         string
	OwnerRef                  config.OwnerRef `json:"ownerRef"`
	OperatorDeploymentConfig  config.DeploymentConfig
	EtcdDeploymentConfig      config.DeploymentConfig
	PersistentVolumeClaimSpec *corev1.PersistentVolumeClaimSpec `json:"persistentVolumeClaimSpec"`
}

var etcdLabels = map[string]string{
	"app": "etcd",
}

func NewEtcdParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *EtcdParams {
	p := &EtcdParams{
		EtcdOperatorImage: images["etcd-operator"],
		OwnerRef:          config.OwnerRefFrom(hcp),
		ClusterVersion:    config.DefaultEtcdClusterVersion,
	}
	p.OperatorDeploymentConfig.Resources = config.ResourcesSpec{
		etcdOperatorContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.OperatorDeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.OperatorDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.OperatorDeploymentConfig.SetControlPlaneIsolation(hcp)
	p.EtcdDeploymentConfig.Resources = config.ResourcesSpec{
		etcdContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("600Mi"),
				corev1.ResourceCPU:    resource.MustParse("300m"),
			},
		},
	}
	p.EtcdDeploymentConfig.Scheduling.PriorityClass = config.EtcdPriorityClass
	p.EtcdDeploymentConfig.SetColocationAnchor(hcp)
	p.EtcdDeploymentConfig.SetControlPlaneIsolation(hcp)
	p.OperatorDeploymentConfig.Replicas = 1
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.EtcdDeploymentConfig.Replicas = 3
		p.EtcdDeploymentConfig.SetMultizoneSpread(etcdLabels)
	default:
		p.EtcdDeploymentConfig.Replicas = 1
	}
	if hcp.Spec.Etcd.ManagementType == hyperv1.Managed && hcp.Spec.Etcd.Managed != nil {
		p.PersistentVolumeClaimSpec = hcp.Spec.Etcd.Managed.PersistentVolumeClaimSpec
	}
	return p
}
