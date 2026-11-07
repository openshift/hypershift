package manifests

import (
	supportmanifests "github.com/openshift/hypershift/support/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OpenStackProviderConfig is a re-export for backward compatibility.
// New code should import from support/manifests directly.
var OpenStackProviderConfig = supportmanifests.OpenStackProviderConfig

func OpenStackTrustedCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openstack-trusted-ca",
			Namespace: ns,
		},
	}
}
