package scheduler

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type KubeSchedulerParams struct {
	FeatureGate             *configv1.FeatureGateSpec `json:"featureGate"`
	Scheduler               *configv1.SchedulerSpec   `json:"scheduler"`
	OwnerRef                config.OwnerRef           `json:"ownerRef"`
	HyperkubeImage          string                    `json:"hyperkubeImage"`
	AvailabilityProberImage string                    `json:"availabilityProberImage"`
	config.DeploymentConfig `json:",inline"`
	APIServer               *configv1.APIServerSpec `json:"apiServer"`
	DisableProfiling        bool                    `json:"disableProfiling"`
}

func NewKubeSchedulerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, setDefaultSecurityContext bool) *KubeSchedulerParams {
	params := &KubeSchedulerParams{
		HyperkubeImage:          images["hyperkube"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
	}
	if hcp.Spec.Configuration != nil {
		params.FeatureGate = hcp.Spec.Configuration.FeatureGate
		params.Scheduler = hcp.Spec.Configuration.Scheduler
		params.APIServer = hcp.Spec.Configuration.APIServer
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
					Port:   intstr.FromInt(schedulerSecurePort),
					Scheme: corev1.URISchemeHTTPS,
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
					Port:   intstr.FromInt(schedulerSecurePort),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	params.DeploymentConfig.SetDefaults(hcp, labels, nil)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.SetDefaultSecurityContext = setDefaultSecurityContext
	params.DisableProfiling = util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], manifests.SchedulerDeployment("").Name)

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *KubeSchedulerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{FeatureSet: configv1.Default})
	}
}

func (p *KubeSchedulerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}

func (p *KubeSchedulerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *KubeSchedulerParams) SchedulerPolicy() configv1.ConfigMapNameReference {
	if p.Scheduler != nil {
		return p.Scheduler.Policy
	} else {
		return configv1.ConfigMapNameReference{}
	}
}
