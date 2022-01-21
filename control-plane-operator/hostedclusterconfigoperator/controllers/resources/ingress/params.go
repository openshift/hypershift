package ingress

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/globalconfig"
)

type IngressParams struct {
	IngressSubdomain string
	Replicas         int32
	PlatformType     hyperv1.PlatformType
}

func NewIngressParams(hcp *hyperv1.HostedControlPlane, globalConfig globalconfig.GlobalConfig) *IngressParams {
	var replicas int32 = 2
	if hcp.Spec.InfrastructureAvailabilityPolicy == hyperv1.SingleReplica {
		replicas = 1
	}
	return &IngressParams{
		IngressSubdomain: globalconfig.IngressDomain(hcp, globalConfig.Ingress),
		Replicas:         replicas,
		PlatformType:     hcp.Spec.Platform.Type,
	}
}
