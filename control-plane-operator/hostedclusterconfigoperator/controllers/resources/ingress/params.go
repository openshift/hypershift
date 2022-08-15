package ingress

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
)

type IngressParams struct {
	IngressSubdomain string
	Replicas         int32
	PlatformType     hyperv1.PlatformType
	IsPrivate        bool
	IBMCloudUPI      bool
}

func NewIngressParams(hcp *hyperv1.HostedControlPlane) *IngressParams {
	var replicas int32 = 1
	isPrivate := false
	ibmCloudUPI := false
	if hcp.Spec.Platform.IBMCloud != nil && hcp.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
		ibmCloudUPI = true
	}
	if hcp.Annotations[hyperv1.PrivateIngressControllerAnnotation] == "true" {
		isPrivate = true
	}
	if hcp.Spec.InfrastructureAvailabilityPolicy == hyperv1.HighlyAvailable {
		replicas = 2
	}

	return &IngressParams{
		IngressSubdomain: globalconfig.IngressDomain(hcp),
		Replicas:         replicas,
		PlatformType:     hcp.Spec.Platform.Type,
		IsPrivate:        isPrivate,
		IBMCloudUPI:      ibmCloudUPI,
	}

}
