package configoperator

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type HostedClusterConfigOperatorParams struct {
	config.DeploymentConfig
	config.OwnerRef
	Image                   string
	ReleaseImage            string
	OpenShiftVersion        string
	KubernetesVersion       string
	AvailabilityProberImage string
}

func NewHostedClusterConfigOperatorParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, openShiftVersion, kubernetesVersion string) *HostedClusterConfigOperatorParams {
	params := &HostedClusterConfigOperatorParams{
		Image:                   images["hosted-cluster-config-operator"],
		ReleaseImage:            hcp.Spec.ReleaseImage,
		OwnerRef:                config.OwnerRefFrom(hcp),
		OpenShiftVersion:        openShiftVersion,
		KubernetesVersion:       kubernetesVersion,
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
	}
	params.Replicas = 1
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		hccContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("60m"),
			},
		},
	}

	params.LivenessProbes = config.LivenessProbes{
		hccContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(6060),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 60,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    5,
			TimeoutSeconds:      5,
		},
	}
	params.ReadinessProbes = config.ReadinessProbes{
		hccContainerMain().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/readyz",
					Port:   intstr.FromInt(6060),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)

	return params
}
