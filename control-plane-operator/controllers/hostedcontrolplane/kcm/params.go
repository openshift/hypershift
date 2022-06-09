package kcm

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type KubeControllerManagerParams struct {
	FeatureGate         *configv1.FeatureGate        `json:"featureGate"`
	ServiceCA           []byte                       `json:"serviceCA"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`
	CloudProviderCreds  *corev1.LocalObjectReference `json:"cloudProviderCreds"`
	Port                int32                        `json:"port"`
	APIServer           *configv1.APIServer          `json:"apiServer"`
	PlatformType        hyperv1.PlatformType         `json:"platformType"`
	ServiceCIDR         string
	PodCIDR             string

	config.DeploymentConfig
	config.OwnerRef
	HyperkubeImage          string `json:"hyperkubeImage"`
	AvailabilityProberImage string `json:"availabilityProberImage"`
	TokenMinterImage        string `json:"tokenMinterImage"`
}

const (
	DefaultPort = 10257
)

func NewKubeControllerManagerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig, images map[string]string, setDefaultSecurityContext bool) *KubeControllerManagerParams {
	params := &KubeControllerManagerParams{
		FeatureGate: globalConfig.FeatureGate,
		// TODO: Come up with sane defaults for scheduling APIServer pods
		// Expose configuration
		HyperkubeImage:          images["hyperkube"],
		TokenMinterImage:        images["token-minter"],
		Port:                    DefaultPort,
		ServiceCIDR:             hcp.Spec.ServiceCIDR,
		PodCIDR:                 hcp.Spec.PodCIDR,
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		APIServer:               globalConfig.APIServer,
	}
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.LivenessProbes = config.LivenessProbes{
		kcmContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.Port)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 45,
			TimeoutSeconds:      10,
			PeriodSeconds:       10,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		kcmContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(int(params.Port)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      10,
			PeriodSeconds:       10,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		kcmContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("400Mi"),
				corev1.ResourceCPU:    resource.MustParse("60m"),
			},
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)
	params.PlatformType = hcp.Spec.Platform.Type
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AWSProviderConfig("").Name}
		params.CloudProviderCreds = &corev1.LocalObjectReference{Name: hcp.Spec.Platform.AWS.KubeCloudControllerCreds.Name}
	case hyperv1.AzurePlatform:
		params.CloudProvider = azure.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AzureProviderConfigWithCredentials("").Name}
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
		params.DeploymentConfig.SetMultizoneSpread(kcmLabels())
	default:
		params.Replicas = 1
	}

	params.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *KubeControllerManagerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.Spec.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *KubeControllerManagerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}

func (p *KubeControllerManagerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}
