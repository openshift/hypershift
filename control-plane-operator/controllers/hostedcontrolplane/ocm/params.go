package ocm

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
)

type OpenShiftControllerManagerParams struct {
	OpenShiftControllerManagerImage string
	DockerBuilderImage              string
	DeployerImage                   string
	APIServer                       *configv1.APIServerSpec
	Network                         *configv1.NetworkSpec
	Build                           *configv1.Build
	Image                           *configv1.ImageSpec

	DeploymentConfig config.DeploymentConfig
	config.OwnerRef
}

func NewOpenShiftControllerManagerParams(hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) *OpenShiftControllerManagerParams {
	params := &OpenShiftControllerManagerParams{
		OpenShiftControllerManagerImage: releaseImageProvider.GetImage("openshift-controller-manager"),
		DockerBuilderImage:              releaseImageProvider.GetImage("docker-builder"),
		DeployerImage:                   releaseImageProvider.GetImage("deployer"),
		Build:                           observedConfig.Build,
	}
	if hcp.Spec.Configuration != nil {
		params.APIServer = hcp.Spec.Configuration.APIServer
		params.Network = hcp.Spec.Configuration.Network
		params.Image = hcp.Spec.Configuration.Image
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
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	replicas := ptr.To(2)
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		replicas = ptr.To(1)
	}
	params.DeploymentConfig.SetDefaults(hcp, openShiftControllerManagerLabels(), replicas)
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.DeploymentConfig.LivenessProbes = config.LivenessProbes{
		ocmContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(int(servingPort)),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			InitialDelaySeconds: 30,
			TimeoutSeconds:      5,
		},
	}
	params.DeploymentConfig.ReadinessProbes = config.ReadinessProbes{
		ocmContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(int(servingPort)),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			FailureThreshold: 10,
			TimeoutSeconds:   5,
		},
	}

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *OpenShiftControllerManagerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OpenShiftControllerManagerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}
