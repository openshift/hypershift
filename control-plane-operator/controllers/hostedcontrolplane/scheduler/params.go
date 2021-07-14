package scheduler

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type KubeSchedulerParams struct {
	FeatureGate             *configv1.FeatureGate `json:"featureGate"`
	Scheduler               *configv1.Scheduler   `json:"scheduler"`
	OwnerRef                config.OwnerRef       `json:"ownerRef"`
	HyperkubeImage          string                `json:"hyperkubeImage"`
	config.DeploymentConfig `json:",inline"`
}

func NewKubeSchedulerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, globalConfig config.GlobalConfig) *KubeSchedulerParams {
	params := &KubeSchedulerParams{
		FeatureGate:    globalConfig.FeatureGate,
		Scheduler:      globalConfig.Scheduler,
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
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{FeatureSet: configv1.Default})
	}
}

func (p *KubeSchedulerParams) SchedulerPolicy() configv1.ConfigMapNameReference {
	if p.Scheduler != nil {
		return p.Scheduler.Spec.Policy
	} else {
		return configv1.ConfigMapNameReference{}
	}
}
