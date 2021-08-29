package konnectivity

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
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

func NewKonnectivityParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32) *KonnectivityParams {
	p := &KonnectivityParams{
		KonnectivityServerImage: images["konnectivity-server"],
		KonnectivityAgentImage:  images["konnectivity-agent"],
		ExternalAddress:         externalAddress,
		ExternalPort:            externalPort,
		OwnerRef:                config.OwnerRefFrom(hcp),
	}
	p.ServerDeploymentConfig.LivenessProbes = config.LivenessProbes{
		konnectivityServerContainer().Name: {
			Handler: corev1.Handler{
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
	p.ServerDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityServerContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.ServerDeploymentConfig.Replicas = 1
	p.ServerDeploymentConfig.SetColocation(hcp)
	p.ServerDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.ServerDeploymentConfig.SetControlPlaneIsolation(hcp)

	p.AgentDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("40m"),
			},
		},
	}
	p.AgentDeploymentConfig.LivenessProbes = config.LivenessProbes{
		konnectivityAgentContainer().Name: {
			Handler: corev1.Handler{
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
	p.AgentDeploymentConfig.Replicas = 1
	p.AgentDeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.AgentDeploymentConfig.SetColocation(hcp)
	p.AgentDeploymentConfig.SetControlPlaneIsolation(hcp)

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
			Handler: corev1.Handler{
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
