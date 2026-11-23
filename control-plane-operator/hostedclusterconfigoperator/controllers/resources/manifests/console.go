package manifests

import (
	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsoleOperatorConfig returns the guest operator.openshift.io/v1 Console
// singleton named "cluster".
func ConsoleOperatorConfig() *operatorv1.Console {
	return &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

// ConsoleAuthProxyTrustedCAConfigMap returns the dedicated guest
// openshift-config ConfigMap that holds the CA bundle for console's
// auth-scoped (External OIDC) proxy. A dedicated name is used so this CM is
// never confused with the cluster-wide proxy trustedCA ConfigMap managed
// separately in the same namespace.
func ConsoleAuthProxyTrustedCAConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console-auth-proxy-trusted-ca",
			Namespace: "openshift-config",
		},
	}
}
