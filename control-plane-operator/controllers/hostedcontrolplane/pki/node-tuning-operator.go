package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
)

// Create TLS keys for performance-addon-operator-webhook-cert.
func ReconcilePerformanceAddonOperatorWebhook(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"performance-addon-operator-webhook-cert",
		fmt.Sprintf("performance-addon-operator-webhook-cert.%s.svc", secret.Namespace),
		fmt.Sprintf("performance-addon-operator-webhook-cert.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "performance-addon-operator", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
