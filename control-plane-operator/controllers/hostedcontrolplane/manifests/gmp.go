package manifests

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// GMPCollectorNamespace is the namespace Google's managed collectors run in
	// on GKE.
	GMPCollectorNamespace = "gke-gmp-system"
	// GMPCollectorServiceAccountName is the collector's ServiceAccount. It is the
	// identity that reads the Secrets a PodMonitoring references.
	GMPCollectorServiceAccountName = "collector"
)

// GMPCollectorSecretNames lists the Secrets staged GMP monitors reference for
// TLS. The collector Role grants access to exactly these names: a PodMonitoring
// TLS block can only reference a Secret, and the CA signer Secrets are
// deliberately absent because they also hold private keys.
var GMPCollectorSecretNames = []string{
	"root-ca-cert",
	"etcd-ca-cert",
	"metrics-client",
	"etcd-metrics-client-tls",
}

// GMPCollectorRole is the namespaced Role that grants the GMP collector read
// access to the TLS Secrets in the control-plane namespace.
func GMPCollectorRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gmp-collector-secret-access",
			Namespace: ns,
		},
	}
}

// GMPCollectorRoleBinding binds GMPCollectorRole to the collector ServiceAccount.
// It is constructed in Go rather than as a component asset because the component
// framework rewrites ServiceAccount subject namespaces to the control-plane
// namespace, which would point the binding at a ServiceAccount that does not
// exist there.
func GMPCollectorRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gmp-collector-secret-access",
			Namespace: ns,
		},
	}
}
