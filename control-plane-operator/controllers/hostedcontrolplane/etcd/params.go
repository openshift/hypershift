package etcd

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type EtcdParams struct {
	ClusterVersion           string
	EtcdOperatorImage        string
	OwnerRef                 config.OwnerRef `json:"ownerRef"`
	OperatorDeploymentConfig config.DeploymentConfig
	EtcdDeploymentConfig     config.DeploymentConfig
	PVCClaim                 *corev1.PersistentVolumeClaimSpec `json:"pvcClaim"`
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
	p.EtcdDeploymentConfig.Resources = config.ResourcesSpec{
		etcdContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("600Mi"),
				corev1.ResourceCPU:    resource.MustParse("300m"),
			},
		},
	}
	p.OperatorDeploymentConfig.Replicas = 1
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		p.EtcdDeploymentConfig.Replicas = 3
	default:
		p.EtcdDeploymentConfig.Replicas = 1
	}
	return p
}
