package util

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

// HasPrivateAPIServerConnectivity determines if workloads running inside the guest cluster can access
// the apiserver without using the Internet.
func ConnectsThroughInternetToControlplane(platform hyperv1.PlatformSpec) bool {
	return platform.AWS == nil || platform.AWS.EndpointAccess == hyperv1.Public
}
