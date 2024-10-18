package routecm

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
)

type OpenShiftRouteControllerManagerParams struct {
	OpenShiftControllerManagerImage string
	APIServer                       *configv1.APIServerSpec
	Network                         *configv1.NetworkSpec

	DeploymentConfig config.DeploymentConfig
	config.OwnerRef
}

func NewOpenShiftRouteControllerManagerParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider *imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) *OpenShiftRouteControllerManagerParams {
	params := &OpenShiftRouteControllerManagerParams{
		OpenShiftControllerManagerImage: releaseImageProvider.GetImage("route-controller-manager"),
	}
	if hcp.Spec.Configuration != nil {
		params.APIServer = hcp.Spec.Configuration.APIServer
		params.Network = hcp.Spec.Configuration.Network
	}

	params.DeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		Resources: map[string]corev1.ResourceRequirements{
			routeOCMContainerMain().Name: {
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
	replicas := pointer.Int(2)
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		replicas = pointer.Int(1)
	}
	params.DeploymentConfig.SetDefaults(hcp, openShiftRouteControllerManagerLabels(), replicas)
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OwnerRef = config.OwnerRefFrom(hcp)

	params.DeploymentConfig.LivenessProbes = config.LivenessProbes{
		routeOCMContainerMain().Name: {
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
		routeOCMContainerMain().Name: {
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

	return params
}

func (p *OpenShiftRouteControllerManagerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}

func (p *OpenShiftRouteControllerManagerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}
