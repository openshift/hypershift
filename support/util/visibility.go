package util

import (
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

const managedServiceEnvVar = "MANAGED_SERVICE"

func isAroHCP() bool {
	return os.Getenv(managedServiceEnvVar) == hyperv1.AroHCP
}

func IsPrivateHCP(hcp *hyperv1.HostedControlPlane) bool {
	// ARO always have swift enabled.
	// We still check the annotation to keep CI working.
	// TODO(alberto): Remove this once CI has swift enabled.
	if isAroHCP() && hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		return true
	}
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}

	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hcp.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	return false
}

func IsPublicHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Public
	}
	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hcp.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate
	}
	return true
}

func IsPrivateHC(hc *hyperv1.HostedCluster) bool {
	// ARO always have swift enabled.
	// We still check the annotation to keep CI working.
	// TODO(alberto): Remove this once CI has swift enabled.
	if isAroHCP() && hc.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		return true
	}
	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}
	if hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hc.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	return false
}

func IsPublicHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Public
	}
	if hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hc.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate
	}
	return true
}

func IsPublicWithDNS(hcp *hyperv1.HostedControlPlane) bool {
	return IsPublicHCP(hcp) && (UseDedicatedDNS(hcp, hyperv1.APIServer) ||
		UseDedicatedDNS(hcp, hyperv1.OAuthServer) ||
		UseDedicatedDNS(hcp, hyperv1.Konnectivity) ||
		UseDedicatedDNS(hcp, hyperv1.Ignition))
}

func IsPublicWithDNSByHC(hc *hyperv1.HostedCluster) bool {
	return IsPublicHC(hc) && (UseDedicatedDNSByHC(hc, hyperv1.APIServer) ||
		UseDedicatedDNSByHC(hc, hyperv1.OAuthServer) ||
		UseDedicatedDNSByHC(hc, hyperv1.Konnectivity) ||
		UseDedicatedDNSByHC(hc, hyperv1.Ignition))
}
