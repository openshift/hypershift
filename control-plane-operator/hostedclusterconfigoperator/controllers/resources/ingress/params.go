package ingress

import (
	configv1 "github.com/openshift/api/config/v1"
	v1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
)

type IngressParams struct {
	IngressSubdomain  string
	Replicas          int32
	PlatformType      hyperv1.PlatformType
	IsPrivate         bool
	IBMCloudUPI       bool
	AWSNLB            bool
	LoadBalancerScope v1.LoadBalancerScope
	LoadBalancerIP    string
}

func NewIngressParams(hcp *hyperv1.HostedControlPlane) *IngressParams {
	var replicas int32 = 1
	isPrivate := false
	ibmCloudUPI := false
	nlb := false
	var loadBalancerIP string
	loadBalancerScope := v1.ExternalLoadBalancer
	if hcp.Spec.Platform.IBMCloud != nil && hcp.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
		ibmCloudUPI = true
	}
	if hcp.Annotations[hyperv1.PrivateIngressControllerAnnotation] == "true" {
		isPrivate = true
	}
	if hcp.Annotations[hyperv1.IngressControllerLoadBalancerScope] == string(v1.InternalLoadBalancer) {
		loadBalancerScope = v1.InternalLoadBalancer
	}
	if hcp.Spec.InfrastructureAvailabilityPolicy == hyperv1.HighlyAvailable {
		replicas = 2
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Ingress != nil && hcp.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS != nil {
		nlb = hcp.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type == configv1.NLB
		if hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.Private {
			loadBalancerScope = v1.InternalLoadBalancer
		}
	}
	if hcp.Spec.Platform.OpenStack != nil && hcp.Spec.Platform.OpenStack.IngressFloatingIP != "" {
		loadBalancerIP = hcp.Spec.Platform.OpenStack.IngressFloatingIP
	}

	return &IngressParams{
		IngressSubdomain:  globalconfig.IngressDomain(hcp),
		Replicas:          replicas,
		PlatformType:      hcp.Spec.Platform.Type,
		IsPrivate:         isPrivate,
		IBMCloudUPI:       ibmCloudUPI,
		AWSNLB:            nlb,
		LoadBalancerScope: loadBalancerScope,
		LoadBalancerIP:    loadBalancerIP,
	}
}
