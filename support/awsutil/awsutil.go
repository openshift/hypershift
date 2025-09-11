package awsutil

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// IsROSAHCP returns true if the hosted control plane is a ROSA (Red Hat OpenShift Service on AWS) cluster
// This is determined by checking for the red-hat-managed tag set to "true"
func IsROSAHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.AWS == nil {
		return false
	}

	return HasResourceTag(hcp, "red-hat-managed", "true")
}

// HasResourceTag returns true if the hosted control plane has a specific resource tag with the given key and value
func HasResourceTag(hcp *hyperv1.HostedControlPlane, key, value string) bool {
	if hcp.Spec.Platform.AWS == nil {
		return false
	}

	for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
		if tag.Key == key && tag.Value == value {
			return true
		}
	}

	return false
}

// GetResourceTagValue returns the value of a specific resource tag key, or empty string if not found
func GetResourceTagValue(hcp *hyperv1.HostedControlPlane, key string) string {
	if hcp.Spec.Platform.AWS == nil {
		return ""
	}

	for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
		if tag.Key == key {
			return tag.Value
		}
	}

	return ""
}

// HasResourceTagKey returns true if the hosted control plane has a resource tag with the given key (any value)
func HasResourceTagKey(hcp *hyperv1.HostedControlPlane, key string) bool {
	if hcp.Spec.Platform.AWS == nil {
		return false
	}

	for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
		if tag.Key == key {
			return true
		}
	}

	return false
}
