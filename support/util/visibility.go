package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func IsPrivateHCP(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Spec.Platform.Type == hyperv1.AWSPlatform &&
		(hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate ||
			hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.Private)
}

func IsPublicHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return true
	}
	return hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate ||
		hcp.Spec.Platform.AWS.EndpointAccess == hyperv1.Public
}

func IsPrivateHC(hc *hyperv1.HostedCluster) bool {
	return hc.Spec.Platform.Type == hyperv1.AWSPlatform &&
		(hc.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate ||
			hc.Spec.Platform.AWS.EndpointAccess == hyperv1.Private)
}
