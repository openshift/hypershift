package konnectivity

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"

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

func NewKonnectivityParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32, explicitNonRootSecurityContext bool) *KonnectivityParams {
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
	p.ServerDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityServerContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.ServerDeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
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
	p.AgentDeploymentConfig.Replicas = 1
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		p.AgentDeploymentConfig.Replicas = 3
	}
	p.AgentDeploymentConfig.SetMultizoneSpread(konnectivityAgentLabels())
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

	if explicitNonRootSecurityContext {
		// iterate over resources and set security context to all the containers
		agentDeamonSecurityContextsObj := make(config.SecurityContextSpec)
		for containerName := range p.AgentDeamonSetConfig.Resources {
			agentDeamonSecurityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		p.AgentDeamonSetConfig.SecurityContexts = agentDeamonSecurityContextsObj

		agentDeploymentSecurityContextsObj := make(config.SecurityContextSpec)
		for containerName := range p.AgentDeploymentConfig.Resources {
			agentDeploymentSecurityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		p.AgentDeploymentConfig.SecurityContexts = agentDeploymentSecurityContextsObj

		serverDeploymentSecurityContextsObj := make(config.SecurityContextSpec)
		for containerName := range p.ServerDeploymentConfig.Resources {
			serverDeploymentSecurityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		p.ServerDeploymentConfig.SecurityContexts = serverDeploymentSecurityContextsObj
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
