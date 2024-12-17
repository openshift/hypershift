package konnectivity

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	healthPort                      = 2041
	systemNodeCriticalPriorityClass = "system-node-critical"
)

type KonnectivityParams struct {
	KonnectivityAgentImage string
	OwnerRef               config.OwnerRef
	AgentDeploymentConfig  config.DeploymentConfig
	AgentDeamonSetConfig   config.DeploymentConfig
}

func NewKonnectivityParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, externalAddress string, externalPort int32, setDefaultSecurityContext bool) *KonnectivityParams {
	p := &KonnectivityParams{
		KonnectivityAgentImage: releaseImageProvider.GetImage("apiserver-network-proxy"),
		OwnerRef:               config.OwnerRefFrom(hcp),
	}

	p.AgentDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("40m"),
			},
		},
	}
	p.AgentDeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.AgentDeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.AgentDeploymentConfig.LivenessProbes = config.LivenessProbes{
		konnectivityAgentContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			TimeoutSeconds:      30,
			PeriodSeconds:       60,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}

	p.AgentDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.AgentDeploymentConfig.SetDefaults(hcp, konnectivityAgentLabels(), nil)
	p.AgentDeamonSetConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("40m"),
			},
		},
	}
	p.AgentDeamonSetConfig.Scheduling = config.Scheduling{
		PriorityClass: systemNodeCriticalPriorityClass,
	}
	p.AgentDeamonSetConfig.LivenessProbes = config.LivenessProbes{
		konnectivityAgentContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			TimeoutSeconds:      30,
			PeriodSeconds:       60,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}

	// non root security context if scc capability is missing
	p.AgentDeamonSetConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	p.AgentDeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	if _, ok := hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]; ok {
		p.KonnectivityAgentImage = hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]
	}

	return p
}

type KonnectivityServiceParams struct {
	OwnerRef config.OwnerRef
}

func NewKonnectivityServiceParams(hcp *hyperv1.HostedControlPlane) *KonnectivityServiceParams {
	return &KonnectivityServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
