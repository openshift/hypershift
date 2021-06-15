package scheduler

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type KubeSchedulerParams struct {
	FeatureGate             configv1.FeatureGate `json:"featureGate"`
	OwnerRef                config.OwnerRef      `json:"ownerRef"`
	HyperkubeImage          string               `json:"hyperkubeImage"`
	config.DeploymentConfig `json:",inline"`
}

func NewKubeSchedulerParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *KubeSchedulerParams {
	params := &KubeSchedulerParams{
		FeatureGate: configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.Default,
				},
			},
		},
		HyperkubeImage: images["hyperkube"],
	}
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		schedulerContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("150Mi"),
				corev1.ResourceCPU:    resource.MustParse("25m"),
			},
		},
	}
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
	default:
		params.Replicas = 1
	}
	params.OwnerRef = config.OwnerRefFrom(hcp)

	return params
}

func (p *KubeSchedulerParams) FeatureGates() []string {
	return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
}
