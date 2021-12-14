package scheduler

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type KubeSchedulerParams struct {
	FeatureGate             *configv1.FeatureGate `json:"featureGate"`
	Scheduler               *configv1.Scheduler   `json:"scheduler"`
	OwnerRef                config.OwnerRef       `json:"ownerRef"`
	HyperkubeImage          string                `json:"hyperkubeImage"`
	AvailabilityProberImage string                `json:"availabilityProberImage"`
	config.DeploymentConfig `json:",inline"`
}

func NewKubeSchedulerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, globalConfig globalconfig.GlobalConfig, explicitNonRootSecurityContext bool) *KubeSchedulerParams {
	params := &KubeSchedulerParams{
		FeatureGate:             globalConfig.FeatureGate,
		Scheduler:               globalConfig.Scheduler,
		HyperkubeImage:          images["hyperkube"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
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
	params.LivenessProbes = config.LivenessProbes{
		schedulerContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(10251),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 60,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    5,
			TimeoutSeconds:      5,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		schedulerContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(10251),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
		params.DeploymentConfig.SetMultizoneSpread(schedulerLabels)
	default:
		params.Replicas = 1
	}
	if explicitNonRootSecurityContext {
		// iterate over resources and set security context to all the containers
		securityContextsObj := make(config.SecurityContextSpec)
		for containerName := range params.DeploymentConfig.Resources {
			securityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		params.DeploymentConfig.SecurityContexts = securityContextsObj
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
