package manifests

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PullSecret returns a reference to the pull-secret Secret in the given namespace.
func PullSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: ns,
		},
	}
}

// AzureProviderConfig is a configMap for Azure cloud config. This is needed for ignition configuration by the
// machine-config-operator (MCO). https://github.com/openshift/machine-config-operator/blob/fe8353e4ea7e72dfd69105069b870a37a87478ec/pkg/operator/bootstrap.go#L124
func AzureProviderConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-cloud-config",
			Namespace: ns,
		},
	}
}

// OpenStackProviderConfig returns a reference to the OpenStack cloud config ConfigMap.
func OpenStackProviderConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openstack-cloud-config",
			Namespace: ns,
		},
	}
}

const tokenSecretPrefix = "token"

// TokenSecret returns a reference to the token Secret for a NodePool with the given payload hash.
// This is used by both the NodePool controller (to create the secret) and the ignition server
// (to authorize requests and record events).
func TokenSecret(namespace, name, payloadInputHash string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-%s-%s", tokenSecretPrefix, name, payloadInputHash),
		},
	}
}
