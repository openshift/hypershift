package azure

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

type AzureParams struct {
	ClusterID        string                  `json:"clusterID"`
	ClusterNetwork   string                  `json:"clusterNetwork"`
	OwnerRef         *metav1.OwnerReference  `json:"ownerRef"`
	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
}

func NewAzureParams(hcp *hyperv1.HostedControlPlane) *AzureParams {
	if hcp.Spec.Platform.Azure == nil {
		return nil
	}
	p := &AzureParams{
		ClusterID:      hcp.Spec.InfraID,
		ClusterNetwork: hcp.Spec.Networking.ClusterNetwork[0].CIDR.String(),
	}
	p.OwnerRef = config.ControllerOwnerRef(hcp)

	p.DeploymentConfig.SetDefaults(hcp, ccmLabels(), pointer.Int(1))
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		ccmContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("60Mi"),
				corev1.ResourceCPU:    resource.MustParse("75m"),
			},
		},
	}
	p.DeploymentConfig.AdditionalLabels = additionalLabels()
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		p.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaultSecurityContext = false

	return p
}
