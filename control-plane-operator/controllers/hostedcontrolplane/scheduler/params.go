package scheduler

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type KubeSchedulerParams struct {
	FeatureGate configv1.FeatureGate `json:"featureGate"`

	Replicas         int32 `json:"replicas"`
	Scheduling       config.Scheduling
	AdditionalLabels config.AdditionalLabels    `json:"additionalLabels"`
	SecurityContexts config.SecurityContextSpec `json:"securityContexts"`
	LivenessProbes   config.LivenessProbes      `json:"livenessProbes"`
	ReadinessProbes  config.ReadinessProbes     `json:"readinessProbes"`
	Resources        config.ResourcesSpec       `json:"resources"`
	OwnerReference   *metav1.OwnerReference     `json:"ownerReference"`

	HyperkubeImage string `json:"hyperkubeImage"`
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
		AdditionalLabels: map[string]string{},
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		HyperkubeImage: images["hyperkube"],
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
	params.OwnerReference = config.ControllerOwnerRef(hcp)

	return params
}
