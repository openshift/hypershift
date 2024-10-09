package openstack

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

type OpenStackParams struct {
	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
}

func NewOpenStackParams(hcp *hyperv1.HostedControlPlane) *OpenStackParams {
	if hcp.Spec.Platform.OpenStack == nil {
		return nil
	}
	p := &OpenStackParams{}

	p.DeploymentConfig.SetDefaults(hcp, ccmLabels(), ptr.To(1))
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		ccmContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("200m"),
			},
		},
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return p
}
