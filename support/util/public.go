package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// ConnectsThroughInternetToControlplane determines if workloads running inside the guest cluster
// connect through the Internet to reach the control plane.
func ConnectsThroughInternetToControlplane(platform hyperv1.PlatformSpec) bool {
	if platform.AWS != nil {
		return platform.AWS.EndpointAccess == hyperv1.Public
	}
	if platform.Azure != nil {
		return platform.Azure.EndpointAccess == hyperv1.AzureEndpointAccessPublic || platform.Azure.EndpointAccess == ""
	}
	return true
}
