package main

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// resolveNodeClassGroupKind returns the NodeClass group and kind for the HostedControlPlane platform.
// Unsupported platforms return an error.
func resolveNodeClassGroupKind(platform hyperv1.PlatformType) (schema.GroupKind, error) {
	switch platform {
	case hyperv1.AzurePlatform:
		return schema.GroupKind{
			Group: "karpenter.hypershift.openshift.io",
			Kind:  "OpenShiftAzureNodeClass",
		}, nil
	case hyperv1.AWSPlatform:
		return schema.GroupKind{
			Group: "karpenter.hypershift.openshift.io",
			Kind:  "OpenshiftEC2NodeClass",
		}, nil
	default:
		return schema.GroupKind{}, fmt.Errorf("unsupported platform %q", platform)
	}
}
