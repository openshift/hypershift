package ingress

import (
	configv1 "github.com/openshift/api/config/v1"
	v1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/types/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
)

type IngressParams struct {
	IngressSubdomain string
	Replicas         int32
	PlatformType     hyperv1.PlatformType
	IsPrivate        bool
	IBMCloudUPI      bool
	AWSNLB           bool
	AWSNLBScope      v1.LoadBalancerScope
}

func NewIngressParams(hcp *hyperv1.HostedControlPlane) *IngressParams {
	var replicas int32 = 1
	isPrivate := false
	ibmCloudUPI := false
	nlb := false
	awsNlbScope := v1.ExternalLoadBalancer
	if hcp.Spec.Platform.IBMCloud != nil && hcp.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
		ibmCloudUPI = true
	}
	if hcp.Annotations[hyperv1.PrivateIngressControllerAnnotation] == "true" {
		isPrivate = true
	}
	if hcp.Spec.InfrastructureAvailabilityPolicy == hyperv1.HighlyAvailable {
		replicas = 2
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil && hcp.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS != nil {
		nlb = hcp.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type == configv1.NLB
		if hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.Private {
			awsNlbScope = v1.InternalLoadBalancer
		}
	}

	return &IngressParams{
		IngressSubdomain: globalconfig.IngressDomain(hcp),
		Replicas:         replicas,
		PlatformType:     hcp.Spec.Platform.Type,
		IsPrivate:        isPrivate,
		IBMCloudUPI:      ibmCloudUPI,
		AWSNLB:           nlb,
		AWSNLBScope:      awsNlbScope,
	}

}
