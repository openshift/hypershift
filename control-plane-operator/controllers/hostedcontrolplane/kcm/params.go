package kcm

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type KubeControllerManagerParams struct {
	FeatureGate         *configv1.FeatureGateSpec    `json:"featureGate"`
	ServiceCA           []byte                       `json:"serviceCA"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`
	CloudProviderCreds  *corev1.LocalObjectReference `json:"cloudProviderCreds"`
	Port                int32                        `json:"port"`
	ServiceCIDR         string
	ClusterCIDR         string
	APIServer           *configv1.APIServerSpec `json:"apiServer"`
	DisableProfiling    bool                    `json:"disableProfiling"`

	config.DeploymentConfig
	config.OwnerRef
	HyperkubeImage          string `json:"hyperkubeImage"`
	AvailabilityProberImage string `json:"availabilityProberImage"`
	TokenMinterImage        string `json:"tokenMinterImage"`
}

const (
	DefaultPort = 10257
)

func NewKubeControllerManagerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) *KubeControllerManagerParams {
	params := &KubeControllerManagerParams{
		// TODO: Come up with sane defaults for scheduling APIServer pods
		// Expose configuration
		HyperkubeImage:          releaseImageProvider.GetImage("hyperkube"),
		TokenMinterImage:        releaseImageProvider.GetImage("token-minter"),
		Port:                    DefaultPort,
		ServiceCIDR:             util.FirstServiceCIDR(hcp.Spec.Networking.ServiceNetwork),
		ClusterCIDR:             util.FirstClusterCIDR(hcp.Spec.Networking.ClusterNetwork),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
	}

	// This value comes from the Cloud Provider Azure documentation: https://cloud-provider-azure.sigs.k8s.io/install/azure-ccm/#kube-controller-manager
	if hcp.Spec.Platform.Type == hyperv1.AzurePlatform {
		params.CloudProvider = "external"
	}

	if hcp.Spec.Configuration != nil {
		params.FeatureGate = hcp.Spec.Configuration.FeatureGate
		params.APIServer = hcp.Spec.Configuration.APIServer
	}

	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.DisableProfiling = util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], manifests.KCMDeployment("").Name)
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
			TimeoutSeconds:   10,
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			FailureThreshold: 3,
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
	params.DeploymentConfig.SetDefaults(hcp, kcmLabels(), nil)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	params.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *KubeControllerManagerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(&p.FeatureGate.FeatureGateSelection)
	} else {
		return config.FeatureGates(&configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *KubeControllerManagerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	}
	return config.CipherSuites(nil)
}

func (p *KubeControllerManagerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	}
	return config.MinTLSVersion(nil)
}
