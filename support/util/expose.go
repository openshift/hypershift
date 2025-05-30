package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func ServicePublishingStrategyByTypeForHCP(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func IsLBKAS(hcp *hyperv1.HostedControlPlane) bool {
	apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.LoadBalancer
}

func IsRouteKAS(hcp *hyperv1.HostedControlPlane) bool {
	apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.Route
}

func IsRouteOAuth(hcp *hyperv1.HostedControlPlane) bool {
	oauthService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	return oauthService != nil && oauthService.Type == hyperv1.Route
}

func IsRouteKonnectivity(hcp *hyperv1.HostedControlPlane) bool {
	konnectivityService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	return konnectivityService != nil && konnectivityService.Type == hyperv1.Route
}

func IsRouteIgnition(hcp *hyperv1.HostedControlPlane) bool {
	ignitionService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Ignition)
	return ignitionService != nil && ignitionService.Type == hyperv1.Route
}

func UseDedicatedDNSforKAS(hcp *hyperv1.HostedControlPlane) bool {
	apiServerService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	return IsRouteKAS(hcp) &&
		// When using dedicated DNS apiServerService.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		apiServerService.Route != nil && apiServerService.Route.Hostname != ""
}

func UseDedicatedDNSForOAuth(hcp *hyperv1.HostedControlPlane) bool {
	oauthService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	return IsRouteOAuth(hcp) && oauthService.Route != nil && oauthService.Route.Hostname != ""
}

func UseDedicatedDNSForKonnectivity(hcp *hyperv1.HostedControlPlane) bool {
	konnService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Konnectivity)
	return IsRouteKonnectivity(hcp) && konnService.Route != nil && konnService.Route.Hostname != ""
}

func UseDedicatedDNSForIgnition(hcp *hyperv1.HostedControlPlane) bool {
	ignitionService := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Ignition)
	return IsRouteIgnition(hcp) && ignitionService.Route != nil && ignitionService.Route.Hostname != ""
}

func ServicePublishingStrategyByTypeByHC(hc *hyperv1.HostedCluster, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hc.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func IsLBKASByHC(hc *hyperv1.HostedCluster) bool {
	apiServerService := ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.LoadBalancer
}

func UseDedicatedDNSForKASByHC(hc *hyperv1.HostedCluster) bool {
	apiServerService := ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	return apiServerService != nil && apiServerService.Type == hyperv1.Route &&
		// When using dedicated DNS apiServerService.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		apiServerService.Route != nil && apiServerService.Route.Hostname != ""
}

func ServiceExternalDNSHostname(hcp *hyperv1.HostedControlPlane, serviceType hyperv1.ServiceType) string {
	// external DNS hostname can only be reached when HCP is Public
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

func ServiceExternalDNSHostnameByHC(hc *hyperv1.HostedCluster, serviceType hyperv1.ServiceType) string {
	// external DNS hostname can only be reached when HC is Public
	if !IsPublicHC(hc) {
		return ""
	}

	service := ServicePublishingStrategyByTypeByHC(hc, serviceType)
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
