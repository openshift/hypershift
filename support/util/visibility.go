package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

func IsPrivateHCP(hcp *hyperv1.HostedControlPlane) bool {
	access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
	return hcp.Spec.Platform.Type == hyperv1.AWSPlatform &&
		(access == hyperv1.PublicAndPrivate ||
			access == hyperv1.Private)
}

func IsPublicHCP(hcp *hyperv1.HostedControlPlane) bool {
	access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return true
	}
	return access == hyperv1.PublicAndPrivate ||
		access == hyperv1.Public
}

func IsPrivateHC(hc *hyperv1.HostedCluster) bool {
	access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
	return hc.Spec.Platform.Type == hyperv1.AWSPlatform &&
		(access == hyperv1.PublicAndPrivate ||
			access == hyperv1.Private)
}

func IsPublicHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return true
	}
	access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
	return access == hyperv1.PublicAndPrivate || access == hyperv1.Public
}

func IsPublicWithDNS(hcp *hyperv1.HostedControlPlane) bool {
	return IsPublicHCP(hcp) && (UseDedicatedDNS(hcp, hyperv1.APIServer) ||
		UseDedicatedDNS(hcp, hyperv1.OAuthServer) ||
		UseDedicatedDNS(hcp, hyperv1.Konnectivity) ||
		UseDedicatedDNS(hcp, hyperv1.Ignition))
}

// IsPublicWithExternalDNS checks if any service uses a Route with a hostname that is external to
// the management cluster's default ingress domain. This is used to determine if a dedicated HCP
// router LoadBalancer service is needed. Hostnames that are subdomains of the apps domain are
// served by the management cluster's default router via wildcard DNS.
func IsPublicWithExternalDNS(hcp *hyperv1.HostedControlPlane, defaultIngressDomain string) bool {
	return IsPublicHCP(hcp) && (UseDedicatedDNSWithExternalDomain(hcp, hyperv1.APIServer, defaultIngressDomain) ||
		UseDedicatedDNSWithExternalDomain(hcp, hyperv1.OAuthServer, defaultIngressDomain) ||
		UseDedicatedDNSWithExternalDomain(hcp, hyperv1.Konnectivity, defaultIngressDomain) ||
		UseDedicatedDNSWithExternalDomain(hcp, hyperv1.Ignition, defaultIngressDomain))
}

func IsPublicWithDNSByHC(hc *hyperv1.HostedCluster) bool {
	return IsPublicHC(hc) && (UseDedicatedDNSByHC(hc, hyperv1.APIServer) ||
		UseDedicatedDNSByHC(hc, hyperv1.OAuthServer) ||
		UseDedicatedDNSByHC(hc, hyperv1.Konnectivity) ||
		UseDedicatedDNSByHC(hc, hyperv1.Ignition))
}
