package util

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// CreateKarpenterNodeClass creates a custom NodeClass using the default class's
// spec with an explicit version. The caller must register cleanup for the returned class.
func CreateKarpenterNodeClass(ctx context.Context, client crclient.Client, platform hyperv1.PlatformType, name, version string) (*unstructured.Unstructured, error) {
	if version == "" {
		return nil, fmt.Errorf("a pinned NodeClass requires a version")
	}
	apiVersion := "v1"
	var kind string
	switch platform {
	case hyperv1.AWSPlatform:
		kind = "OpenshiftEC2NodeClass"
	case hyperv1.AzurePlatform:
		apiVersion = "v1alpha1"
		kind = "OpenShiftAzureNodeClass"
	default:
		return nil, fmt.Errorf("unsupported Karpenter platform %q", platform)
	}
	// Azure's OpenShift NodeClass wrapper API is supplied by the standalone karpenter-operator.
	defaultClass := &unstructured.Unstructured{}
	defaultClass.SetGroupVersionKind(schema.GroupVersionKind{Group: "karpenter.hypershift.openshift.io", Version: apiVersion, Kind: kind})
	if err := client.Get(ctx, crclient.ObjectKey{Name: "default"}, defaultClass); err != nil {
		return nil, fmt.Errorf("get default %s: %w", kind, err)
	}
	spec, found, err := unstructured.NestedMap(defaultClass.Object, "spec")
	if err != nil {
		return nil, fmt.Errorf("default %s has no valid spec: %w", kind, err)
	}
	if !found {
		return nil, fmt.Errorf("default %s has no spec", kind)
	}
	spec["version"] = version
	pinnedClass := &unstructured.Unstructured{}
	pinnedClass.SetGroupVersionKind(defaultClass.GroupVersionKind())
	pinnedClass.SetName(name)
	pinnedClass.Object["spec"] = spec
	if err := client.Create(ctx, pinnedClass); err != nil {
		return nil, fmt.Errorf("create pinned %s %s: %w", kind, name, err)
	}
	return pinnedClass, nil
}

// CheckKarpenterNodeUnchanged checks that the original node and NodeClaim still
// exist, are not being deleted, and the NodeClaim has not drifted.
func CheckKarpenterNodeUnchanged(ctx context.Context, client crclient.Client, node *corev1.Node, claim *karpenterv1.NodeClaim) error {
	if node == nil || claim == nil {
		return fmt.Errorf("original node and NodeClaim are required")
	}
	currentClaim := &karpenterv1.NodeClaim{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(claim), currentClaim); err != nil {
		return fmt.Errorf("get original NodeClaim %s: %w", claim.Name, err)
	}
	if currentClaim.UID != claim.UID || !currentClaim.DeletionTimestamp.IsZero() {
		return fmt.Errorf("NodeClaim %s was replaced or is being deleted", claim.Name)
	}
	for _, condition := range currentClaim.Status.Conditions {
		if condition.Type == karpenterv1.ConditionTypeDrifted && condition.Status == metav1.ConditionTrue {
			return fmt.Errorf("NodeClaim %s drifted", claim.Name)
		}
	}
	currentNode := &corev1.Node{}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(node), currentNode); err != nil {
		return fmt.Errorf("get original node %s: %w", node.Name, err)
	}
	if currentNode.UID != node.UID || !currentNode.DeletionTimestamp.IsZero() {
		return fmt.Errorf("node %s was replaced or is being deleted", node.Name)
	}
	return nil
}
