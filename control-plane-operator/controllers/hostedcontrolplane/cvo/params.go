package cvo

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type CVOParams struct {
	ReleaseImage            string
	ControlPlaneImage       string
	CLIImage                string
	AvailabilityProberImage string
	ClusterID               string
	OwnerRef                config.OwnerRef
	DeploymentConfig        config.DeploymentConfig
	PlatformType            hyperv1.PlatformType
	FeatureSet              configv1.FeatureSet
}

func NewCVOParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext, enableCVOManagementClusterMetricsAccess bool) *CVOParams {
	p := &CVOParams{
		CLIImage:                releaseImageProvider.GetImage("cli"),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		ControlPlaneImage:       util.HCPControlPlaneReleaseImage(hcp),
		ReleaseImage:            releaseImageProvider.GetImage("cluster-version-operator"),
		OwnerRef:                config.OwnerRefFrom(hcp),
		ClusterID:               hcp.Spec.ClusterID,
		PlatformType:            hcp.Spec.Platform.Type,
	}
	// fallback to hcp.Spec.ReleaseImage if "cluster-version-operator" image is not available.
	// This could happen for example in local dev enviroments if the "OPERATE_ON_RELEASE_IMAGE" env variable is not set.
	if p.ReleaseImage == "" {
		p.ReleaseImage = hcp.Spec.ReleaseImage
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.FeatureGate != nil {
		p.FeatureSet = hcp.Spec.Configuration.FeatureGate.FeatureSet
	}

	if enableCVOManagementClusterMetricsAccess {
		p.DeploymentConfig.AdditionalLabels = map[string]string{
			config.NeedMetricsServerAccessLabel: "true",
		}
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
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	return p
}
