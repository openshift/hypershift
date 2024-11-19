package aws

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
)

type AWSParams struct {
	Zone             string                  `json:"zone"`
	VPC              string                  `json:"vpc"`
	ClusterID        string                  `json:"clusterID"`
	SubnetID         string                  `json:"subnetID"`
	OwnerRef         *metav1.OwnerReference  `json:"ownerRef"`
	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
}

func NewAWSParams(hcp *hyperv1.HostedControlPlane) *AWSParams {
	if hcp.Spec.Platform.AWS == nil {
		return nil
	}
	p := &AWSParams{
		ClusterID: hcp.Spec.InfraID,
		VPC:       hcp.Spec.Platform.AWS.CloudProviderConfig.VPC,
	}
	if hcp.Spec.Platform.AWS.CloudProviderConfig != nil {
		p.Zone = hcp.Spec.Platform.AWS.CloudProviderConfig.Zone
		if hcp.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
			p.SubnetID = *hcp.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID
		}
	}
	p.OwnerRef = config.ControllerOwnerRef(hcp)

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
	p.DeploymentConfig.SetDefaults(hcp, ccmLabels(), ptr.To(1))
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return p
}
