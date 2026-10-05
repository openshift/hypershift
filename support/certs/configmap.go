package certs

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ConfigMapCABundleResolver returns a resolver for CA bundles referenced by ConfigMap name.
func ConfigMapCABundleResolver(ctx context.Context, reader crclient.Reader, namespace string) func(string) (string, error) {
	return func(name string) (string, error) {
		cm := &corev1.ConfigMap{}
		if err := reader.Get(ctx, crclient.ObjectKey{Name: name, Namespace: namespace}, cm); err != nil {
			return "", fmt.Errorf("failed to get CA configmap %q: %w", name, err)
		}

		if value, keyFound := cm.Data[UserCABundleMapKey]; !keyFound {
			return "", fmt.Errorf("CA configmap %q key %q is missing", name, UserCABundleMapKey)
		} else if len(value) == 0 {
			return "", fmt.Errorf("CA configmap %q key %q is empty", name, UserCABundleMapKey)
		}

		return cm.Data[UserCABundleMapKey], nil
	}
}
