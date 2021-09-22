package cvo

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type CVOParams struct {
	Image            string
	OwnerRef         config.OwnerRef
	DeploymentConfig config.DeploymentConfig
}

func NewCVOParams(hcp *hyperv1.HostedControlPlane) *CVOParams {
	p := &CVOParams{
		Image:    hcp.Spec.ReleaseImage,
		OwnerRef: config.OwnerRefFrom(hcp),
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
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("20m"),
			},
		},
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetColocation(hcp)
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.Replicas = 1
	return p
}
