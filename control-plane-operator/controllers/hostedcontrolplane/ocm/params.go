package ocm

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
)

type OpenShiftControllerManagerParams struct {
	OpenShiftControllerManagerImage string
	DockerBuilderImage              string
	DeployerImage                   string
	APIServer                       *configv1.APIServer
	Network                         *configv1.Network
	Build                           *configv1.Build
	Image                           *configv1.Image

	DeploymentConfig config.DeploymentConfig
	config.OwnerRef
}

func NewOpenShiftControllerManagerParams(hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig, images map[string]string, setDefaultSecurityContext bool) *OpenShiftControllerManagerParams {
	params := &OpenShiftControllerManagerParams{
		OpenShiftControllerManagerImage: images["openshift-controller-manager"],
		DockerBuilderImage:              images["docker-builder"],
		DeployerImage:                   images["deployer"],
		APIServer:                       globalConfig.APIServer,
		Network:                         globalConfig.Network,
		Build:                           globalConfig.Build,
		Image:                           globalConfig.Image,
	}

	params.DeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		Resources: map[string]corev1.ResourceRequirements{
			ocmContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					corev1.ResourceCPU:    resource.MustParse("100m"),
				},
			},
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.DeploymentConfig.Replicas = 3
		params.DeploymentConfig.SetMultizoneSpread(openShiftControllerManagerLabels())
	default:
		params.DeploymentConfig.Replicas = 1
	}

	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *OpenShiftControllerManagerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OpenShiftControllerManagerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}
