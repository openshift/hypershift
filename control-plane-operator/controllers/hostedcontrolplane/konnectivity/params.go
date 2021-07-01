package konnectivity

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	DefaultPriorityClass = "system-node-critical"
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
	p.ServerDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityServerContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.ServerDeploymentConfig.Replicas = 1
	p.AgentDeploymentConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
	}
	p.AgentDeploymentConfig.Replicas = 1
	p.AgentDeamonSetConfig.Resources = config.ResourcesSpec{
		konnectivityAgentContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
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
