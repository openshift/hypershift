package scheduler

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type KubeSchedulerParams struct {
	FeatureGate             configv1.FeatureGate `json:"featureGate"`
	Scheduler               configv1.Scheduler   `json:"scheduler"`
	OwnerRef                config.OwnerRef      `json:"ownerRef"`
	HyperkubeImage          string               `json:"hyperkubeImage"`
	config.DeploymentConfig `json:",inline"`
}

func NewKubeSchedulerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string) *KubeSchedulerParams {
	params := &KubeSchedulerParams{
		FeatureGate: configv1.FeatureGate{
			Spec: configv1.FeatureGateSpec{
				FeatureGateSelection: configv1.FeatureGateSelection{
					FeatureSet: configv1.Default,
				},
			},
		},
		Scheduler: configv1.Scheduler{
			Spec: configv1.SchedulerSpec{
				DefaultNodeSelector: "",
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

	log := ctrl.LoggerFrom(ctx)
	if err := config.ExtractConfigs(hcp, []client.Object{&params.FeatureGate, &params.Scheduler}); err != nil {
		log.Error(err, "Errors encountered extracting configs")
	}

	return params
}

func (p *KubeSchedulerParams) FeatureGates() []string {
	return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
}
