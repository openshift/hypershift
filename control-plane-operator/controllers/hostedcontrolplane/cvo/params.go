package cvo

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilpointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type CVOParams struct {
	Image                   string
	CLIImage                string
	AvailabilityProberImage string
	ClusterID               string
	OwnerRef                config.OwnerRef
	DeploymentConfig        config.DeploymentConfig
	PlatformType            hyperv1.PlatformType
}

func NewCVOParams(hcp *hyperv1.HostedControlPlane, images map[string]string, setDefaultSecurityContext bool) *CVOParams {
	p := &CVOParams{
		CLIImage:                images["cli"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		Image:                   hcp.Spec.ReleaseImage,
		OwnerRef:                config.OwnerRefFrom(hcp),
		ClusterID:               hcp.Spec.ClusterID,
		PlatformType:            hcp.Spec.Platform.Type,
	}
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		cvoContainerPrepPayload().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("20Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		cvoContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("70Mi"),
				corev1.ResourceCPU:    resource.MustParse("20m"),
			},
		},
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass

	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	return p
}
