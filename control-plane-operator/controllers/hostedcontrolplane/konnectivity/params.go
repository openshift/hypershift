package konnectivity

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
)

const (
	healthPort                      = 2041
	systemNodeCriticalPriorityClass = "system-node-critical"
)

type KonnectivityParams struct {
	KonnectivityServerImage string
	KonnectivityAgentImage  string
	ExternalAddress         string
	ExternalPort            int32
	OwnerRef                config.OwnerRef
	ServerDeploymentConfig  config.DeploymentConfig
	AgentDeploymentConfig   config.DeploymentConfig
	AgentDeamonSetConfig    config.DeploymentConfig
}

func NewKonnectivityParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32, setDefaultSecurityContext bool) *KonnectivityParams {
	p := &KonnectivityParams{
		KonnectivityServerImage: images["konnectivity-server"],
		KonnectivityAgentImage:  images["konnectivity-agent"],
		ExternalAddress:         externalAddress,
		ExternalPort:            externalPort,
		OwnerRef:                config.OwnerRefFrom(hcp),
	}
	p.ServerDeploymentConfig.LivenessProbes = config.LivenessProbes{
		konnectivityServerContainer().Name: {
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
	p.ServerDeploymentConfig.ReadinessProbes = config.ReadinessProbes{
		konnectivityServerContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	p.ServerDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityServerContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.ServerDeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.ServerDeploymentConfig.SetDefaults(hcp, nil, pointer.Int(1))
	p.ServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	p.AgentDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("40m"),
			},
		},
	}
	p.AgentDeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
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
	p.ServerDeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	// check apiserver-network-proxy image in ocp payload and use it
	if _, ok := images["apiserver-network-proxy"]; ok {
		p.KonnectivityServerImage = images["apiserver-network-proxy"]
		p.KonnectivityAgentImage = images["apiserver-network-proxy"]
	}

	if hcp.Annotations != nil {
		if _, ok := hcp.Annotations[hyperv1.KonnectivityServerImageAnnotation]; ok {
			p.KonnectivityServerImage = hcp.Annotations[hyperv1.KonnectivityServerImageAnnotation]
		}
		if _, ok := hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]; ok {
			p.KonnectivityAgentImage = hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]
		}
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
