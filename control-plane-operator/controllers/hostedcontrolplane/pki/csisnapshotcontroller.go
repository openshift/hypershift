package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

// Create TLS keys for csi-snapshot-webhook.
// In standalone OCP it's created automatically when csi-snapshot-controller-operator creates Service for
// the webhook with annotation `service.openshift.io/serving-cert-secret-name`, in HyperShift
// it must be done by control-plane-operator.
func ReconcileCSISnapshotWebhookTLS(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"csi-snapshot-webhook",
		fmt.Sprintf("csi-snapshot-webhook.%s.svc", secret.Namespace),
		fmt.Sprintf("csi-snapshot-webhook.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "packageserver", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
