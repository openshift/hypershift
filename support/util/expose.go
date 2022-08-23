package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func ServicePublishingStrategyByTypeForHCP(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func HasPublicLoadBalancerForPrivateRouter(hcp *hyperv1.HostedControlPlane) bool {
	if apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer); apiServerService != nil && apiServerService.Type == hyperv1.Route {
		return true
	}
	return false
}
