package manifests

import (
	supportmanifests "github.com/openshift/hypershift/support/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OpenStackProviderConfig delegates to support/manifests for backward compatibility.
// New code should import from support/manifests directly.
func OpenStackProviderConfig(ns string) *corev1.ConfigMap {
	return supportmanifests.OpenStackProviderConfig(ns)
}

func OpenStackTrustedCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openstack-trusted-ca",
			Namespace: ns,
		},
	}
}
