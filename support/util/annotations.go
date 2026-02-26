package util

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HasAnnotationWithValue checks if a Kubernetes object has a specific annotation with a given value.
func HasAnnotationWithValue(obj metav1.Object, key, expectedValue string) bool {
	annotations := obj.GetAnnotations()
	val, ok := annotations[key]
	return ok && val == expectedValue
}

// HostedClusterFromAnnotation retrieves the HostedCluster referenced by the
// HostedClusterAnnotation on the given object. The annotation value must be in
// "namespace/name" format. This is a shared utility used by platform controllers
// (e.g. Azure PLS, GCP Private Service Connect) whose custom resources carry
// this annotation to link back to their owning HostedCluster.
func HostedClusterFromAnnotation(ctx context.Context, reader client.Reader, obj client.Object) (*hyperv1.HostedCluster, error) {
	hcNamespaceName, exists := obj.GetAnnotations()[HostedClusterAnnotation]
	if !exists {
		return nil, fmt.Errorf("%s %s/%s missing %s annotation", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(), HostedClusterAnnotation)
	}

	parts := strings.SplitN(hcNamespaceName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid %s annotation format: %s", HostedClusterAnnotation, hcNamespaceName)
	}

	hc := &hyperv1.HostedCluster{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: parts[0], Name: parts[1]}, hc); err != nil {
		return nil, fmt.Errorf("failed to get hosted cluster %s/%s: %w", parts[0], parts[1], err)
	}
	return hc, nil
}
