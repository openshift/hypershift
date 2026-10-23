package main

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResolveNodeClassGroupKind returns the NodeClass group and kind for the HostedControlPlane platform.
func resolveNodeClassGroupKind(platform hyperv1.PlatformType) schema.GroupKind {
	switch platform {
	case hyperv1.AzurePlatform:
		return schema.GroupKind{
			Group: "karpenter.hypershift.openshift.io",
			Kind:  "OpenShiftAzureNodeClass",
		}
	default:
		return schema.GroupKind{
			Group: "karpenter.hypershift.openshift.io",
			Kind:  "OpenshiftEC2NodeClass",
		}
	}
}
