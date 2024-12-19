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

func IsPublicKASWithDNS(hostedControlPlane *hyperv1.HostedControlPlane) bool {
	return IsPublicHCP(hostedControlPlane) && UseDedicatedDNSforKAS(hostedControlPlane)
}
