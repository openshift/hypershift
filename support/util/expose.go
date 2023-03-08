package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func ServicePublishingStrategyByTypeForHCP(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func IsRouteKAS(hcp *hyperv1.HostedControlPlane) bool {
	apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.Route
}

func UseDedicatedDNSforKAS(hcp *hyperv1.HostedControlPlane) bool {
	apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	return IsRouteKAS(hcp) &&
		// When using dedicated DNS apiServerService.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		apiServerService.Route != nil && apiServerService.Route.Hostname != ""
}

func ServicePublishingStrategyByTypeByHC(hc *hyperv1.HostedCluster, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hc.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func UseDedicatedDNSForKASByHC(hc *hyperv1.HostedCluster) bool {
	apiServerService := ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.Route &&
		// When using dedicated DNS apiServerService.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		apiServerService.Route != nil && apiServerService.Route.Hostname != ""
}

func ServiceExternalDNSHostname(hcp *hyperv1.HostedControlPlane, serviceType hyperv1.ServiceType) string {
	// external DNS hostname can only be set when HCP is Public
	if !IsPublicHCP(hcp) {
		return ""
	}

	service := ServicePublishingStrategyByTypeForHCP(hcp, serviceType)
	if service == nil {
		return ""
	}

	if service.Type == hyperv1.LoadBalancer && service.LoadBalancer != nil {
		return service.LoadBalancer.Hostname
	}
	if service.Type == hyperv1.Route && service.Route != nil {
		return service.Route.Hostname
	}
	return ""
}
