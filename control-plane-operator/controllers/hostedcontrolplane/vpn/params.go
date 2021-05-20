package vpn

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	DefaultPriorityClass = "system-node-critical"
)

type VPNParams struct {
	Network                      configv1.Network        `json:"network"`
	MachineCIDR                  string                  `json:"computeCIDR"`
	VPNImage                     string                  `json:"vpnImage"`
	ExternalAddress              string                  `json:"externalAddress"`
	ExternalPort                 int32                   `json:"externalPort"`
	ServerDeploymentConfig       config.DeploymentConfig `json:"serverDeploymentConfig"`
	WorkerClientDeploymentConfig config.DeploymentConfig `json:"workerClientDeploymentConfig"`
	OwnerRef                     config.OwnerRef         `json:"ownerRef"`
}

func NewVPNParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32) *VPNParams {
	p := &VPNParams{
		Network:         config.Network(hcp),
		MachineCIDR:     hcp.Spec.MachineCIDR,
		VPNImage:        images["vpn"],
		ExternalAddress: externalAddress,
		ExternalPort:    externalPort,
	}

	p.ServerDeploymentConfig.Replicas = 1
	p.ServerDeploymentConfig.SecurityContexts = config.SecurityContextSpec{
		vpnContainerServer().Name: {
			Privileged: pointer.BoolPtr(true),
		},
	}

	p.WorkerClientDeploymentConfig.Replicas = 1
	p.WorkerClientDeploymentConfig.SecurityContexts = config.SecurityContextSpec{
		vpnContainerClient().Name: {
			Privileged: pointer.BoolPtr(true),
		},
	}
	p.ServerDeploymentConfig.Resources = config.ResourcesSpec{
		vpnContainerServer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			},
		},
	}
	p.ServerDeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: DefaultPriorityClass,
	}
	p.OwnerRef = config.OwnerRefFrom(hcp)
	return p
}

type VPNServiceParams struct {
	OwnerRef config.OwnerRef
}

func NewVPNServiceParams(hcp *hyperv1.HostedControlPlane) *VPNServiceParams {
	return &VPNServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
