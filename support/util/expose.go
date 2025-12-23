package util

import (
	"strings"

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
	return IsRoute(hcp, hyperv1.APIServer)
}

func IsRoute(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) bool {
	svc := ServicePublishingStrategyByTypeForHCP(hcp, svcType)
	return svc != nil && svc.Type == hyperv1.Route
}

func UseDedicatedDNSForKAS(hcp *hyperv1.HostedControlPlane) bool {
	return UseDedicatedDNS(hcp, hyperv1.APIServer)
}

func UseDedicatedDNSForOAuth(hcp *hyperv1.HostedControlPlane) bool {
	return UseDedicatedDNS(hcp, hyperv1.OAuthServer)
}

func UseDedicatedDNSForKonnectivity(hcp *hyperv1.HostedControlPlane) bool {
	return UseDedicatedDNS(hcp, hyperv1.Konnectivity)
}

func UseDedicatedDNSForIgnition(hcp *hyperv1.HostedControlPlane) bool {
	return UseDedicatedDNS(hcp, hyperv1.Ignition)
}

func UseDedicatedDNS(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) bool {
	svc := ServicePublishingStrategyByTypeForHCP(hcp, svcType)
	return IsRoute(hcp, svcType) &&
		// When using dedicated DNS svc.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		svc.Route != nil && svc.Route.Hostname != ""
}

// IsSubdomain returns true if the hostname is a proper subdomain of the given domain.
// It compares DNS labels from right to left, handling case insensitivity.
// For example, "oauth.apps.example.com" is a subdomain of "apps.example.com",
// but "apps.example.com" is not a subdomain of itself.
func IsSubdomain(hostname, domain string) bool {
	if hostname == "" || domain == "" {
		return false
	}
	hostLabels := strings.Split(strings.ToLower(hostname), ".")
	domainLabels := strings.Split(strings.ToLower(domain), ".")

	// hostname must have more labels than domain to be a proper subdomain
	if len(hostLabels) <= len(domainLabels) {
		return false
	}
	// Compare labels from right to left
	for i := 1; i <= len(domainLabels); i++ {
		if hostLabels[len(hostLabels)-i] != domainLabels[len(domainLabels)-i] {
			return false
		}
	}
	return true
}

// UseDedicatedDNSWithExternalDomain checks if a service uses external DNS that requires a dedicated HCP router.
// It returns true only if the service uses a Route with a hostname that is NOT a subdomain of the
// management cluster's default ingress domain. Hostnames under the apps domain are served by the
// management cluster's default router and don't require a separate HCP router LoadBalancer service.
func UseDedicatedDNSWithExternalDomain(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType, defaultIngressDomain string) bool {
	svc := ServicePublishingStrategyByTypeForHCP(hcp, svcType)
	if !IsRoute(hcp, svcType) || svc.Route == nil || svc.Route.Hostname == "" {
		return false
	}
	// If hostname is under the apps domain, it's served by management cluster router
	return !IsSubdomain(svc.Route.Hostname, defaultIngressDomain)
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
	return UseDedicatedDNSByHC(hc, hyperv1.APIServer)
}

func IsRouteByHC(hc *hyperv1.HostedCluster, svcType hyperv1.ServiceType) bool {
	svc := ServicePublishingStrategyByTypeByHC(hc, svcType)
	return svc != nil && svc.Type == hyperv1.Route
}

func UseDedicatedDNSByHC(hc *hyperv1.HostedCluster, svcType hyperv1.ServiceType) bool {
	svc := ServicePublishingStrategyByTypeByHC(hc, svcType)
	return IsRouteByHC(hc, svcType) &&
		// When using dedicated DNS svc.Route.Hostname is set explicitly
		// and later is used to annotate the route so the external DNS controller can watch it.
		svc.Route != nil && svc.Route.Hostname != ""
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
