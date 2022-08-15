package aws

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/config"
)

type AWSParams struct {
	Zone      string                 `json:"zone"`
	VPC       string                 `json:"vpc"`
	ClusterID string                 `json:"clusterID"`
	SubnetID  string                 `json:"subnetID"`
	OwnerRef  *metav1.OwnerReference `json:"ownerRef"`
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

	return p
}
