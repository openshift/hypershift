package manifests

import (
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
func KASEndpoints() *corev1.Endpoints {
	//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: corev1.NamespaceDefault,
		},
	}
}

func KASEndpointSlice() *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: corev1.NamespaceDefault,
		},
	}
}
