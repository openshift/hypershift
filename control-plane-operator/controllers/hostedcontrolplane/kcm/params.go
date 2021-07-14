package kcm

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

type KubeControllerManagerParams struct {
	FeatureGate         *configv1.FeatureGate        `json:"featureGate"`
	ServiceCA           []byte                       `json:"serviceCA"`
	CloudProvider       string                       `json:"cloudProvider"`
	CloudProviderConfig *corev1.LocalObjectReference `json:"cloudProviderConfig"`
	CloudProviderCreds  *corev1.LocalObjectReference `json:"cloudProviderCreds"`
	Port                int32                        `json:"port"`
	ServiceCIDR         string
	PodCIDR             string

	config.DeploymentConfig
	config.OwnerRef
	HyperkubeImage string `json:"hyperkubeImage"`
}

const (
	DefaultPriorityClass = "system-node-critical"
	DefaultPort          = 10257
)

func NewKubeControllerManagerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, globalConfig config.GlobalConfig, images map[string]string) *KubeControllerManagerParams {
	params := &KubeControllerManagerParams{
		FeatureGate: globalConfig.FeatureGate,
		// TODO: Come up with sane defaults for scheduling APIServer pods
		// Expose configuration
		HyperkubeImage: images["hyperkube"],
		Port:           DefaultPort,
		ServiceCIDR:    hcp.Spec.ServiceCIDR,
		PodCIDR:        hcp.Spec.PodCIDR,
	}
	params.AdditionalLabels = map[string]string{}
	params.Scheduling = config.Scheduling{
		PriorityClass: DefaultPriorityClass,
	}
	params.LivenessProbes = config.LivenessProbes{
		kcmContainerMain().Name: {
			Handler: corev1.Handler{
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
			Handler: corev1.Handler{
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
				corev1.ResourceMemory: resource.MustParse("200Mi"),
				corev1.ResourceCPU:    resource.MustParse("60m"),
			},
		},
	}
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		params.CloudProvider = aws.Provider
		params.CloudProviderConfig = &corev1.LocalObjectReference{Name: manifests.AWSProviderConfig("").Name}
		params.CloudProviderCreds = &corev1.LocalObjectReference{Name: hcp.Spec.Platform.AWS.KubeCloudControllerCreds.Name}
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.Replicas = 3
	default:
		params.Replicas = 1
	}
	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func externalAddress(endpoint hyperv1.APIEndpoint) string {
	return fmt.Sprintf("%s:%d", endpoint.Host, endpoint.Port)
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
