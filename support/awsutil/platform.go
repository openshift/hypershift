package awsutil

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// IsROSAHCP returns true if the hosted control plane is a ROSA HCP cluster.
func IsROSAHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.AWS == nil {
		return false
	}

	for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
		if tag.Key == "red-hat-managed" && tag.Value == "true" {
			return true
		}
	}
	return false
}
