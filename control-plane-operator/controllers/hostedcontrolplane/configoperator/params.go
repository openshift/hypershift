package configoperator

import (
	"context"

	utilpointer "k8s.io/utils/pointer"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
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

func NewHostedClusterConfigOperatorParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, openShiftVersion, kubernetesVersion string, setDefaultSecurityContext bool) *HostedClusterConfigOperatorParams {
	params := &HostedClusterConfigOperatorParams{
		Image:                   releaseImageProvider.GetImage("hosted-cluster-config-operator"),
		ReleaseImage:            hcp.Spec.ReleaseImage,
		OwnerRef:                config.OwnerRefFrom(hcp),
		OpenShiftVersion:        openShiftVersion,
		KubernetesVersion:       kubernetesVersion,
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
	}
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		hccContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("80Mi"),
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
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			FailureThreshold: 3,
			TimeoutSeconds:   5,
		},
	}

	params.DeploymentConfig.AdditionalLabels = map[string]string{
		config.NeedManagementKASAccessLabel: "true",
	}
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.Int(1))
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	return params
}
