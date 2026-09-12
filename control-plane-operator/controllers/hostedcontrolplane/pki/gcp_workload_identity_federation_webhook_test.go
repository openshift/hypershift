package pki

import (
	"testing"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

func TestReconcileGCPWorkloadIdentityFederationWebhookServingCert(t *testing.T) {
	t.Run("When reconciling the serving cert, it should generate a valid TLS certificate for 127.0.0.1", func(t *testing.T) {
		namespace := "test-namespace"
		ownerRef := config.OwnerRef{
			Reference: &metav1.OwnerReference{
				APIVersion: "v1",
				Kind:       "HostedControlPlane",
				Name:       "test-hcp",
				UID:        types.UID("test-uid"),
				Controller: ptr.To(true),
			},
		}

		ca := &corev1.Secret{}
		ca.Name = "test-ca"
		ca.Namespace = namespace
		if err := reconcileSelfSignedCA(ca, ownerRef, "test-org", "test-ca"); err != nil {
			t.Fatalf("failed to create CA: %v", err)
		}

		secret := &corev1.Secret{}
		secret.Name = "gcp-workload-identity-federation-webhook-serving-cert"
		secret.Namespace = namespace

		if err := ReconcileGCPWorkloadIdentityFederationWebhookServingCert(secret, ca, ownerRef); err != nil {
			t.Fatalf("failed to reconcile cert: %v", err)
		}

		if secret.Data == nil {
			t.Fatal("secret data is nil")
		}
		if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
			t.Fatal("secret missing tls.crt")
		}
		if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
			t.Fatal("secret missing tls.key")
		}

		cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		if err != nil {
			t.Fatalf("failed to parse certificate: %v", err)
		}

		if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "127.0.0.1" {
			t.Errorf("expected IP SAN [127.0.0.1], got %v", cert.IPAddresses)
		}
		if cert.Subject.CommonName != "127.0.0.1" {
			t.Errorf("expected CN 127.0.0.1, got %s", cert.Subject.CommonName)
		}
	})
}

func TestReconcileGCPWorkloadIdentityFederationWebhookServingCertIdempotent(t *testing.T) {
	t.Run("When reconciling the serving cert twice, it should produce the same certificate", func(t *testing.T) {
		namespace := "test-namespace"
		ownerRef := config.OwnerRef{
			Reference: &metav1.OwnerReference{
				APIVersion: "v1",
				Kind:       "HostedControlPlane",
				Name:       "test-hcp",
				UID:        types.UID("test-uid"),
				Controller: ptr.To(true),
			},
		}

		ca := &corev1.Secret{}
		ca.Name = "test-ca"
		ca.Namespace = namespace
		if err := reconcileSelfSignedCA(ca, ownerRef, "test-org", "test-ca"); err != nil {
			t.Fatalf("failed to create CA: %v", err)
		}

		secret := &corev1.Secret{}
		secret.Name = "gcp-workload-identity-federation-webhook-serving-cert"
		secret.Namespace = namespace

		if err := ReconcileGCPWorkloadIdentityFederationWebhookServingCert(secret, ca, ownerRef); err != nil {
			t.Fatalf("first reconcile failed: %v", err)
		}
		firstCert := string(secret.Data[corev1.TLSCertKey])

		if err := ReconcileGCPWorkloadIdentityFederationWebhookServingCert(secret, ca, ownerRef); err != nil {
			t.Fatalf("second reconcile failed: %v", err)
		}
		if firstCert != string(secret.Data[corev1.TLSCertKey]) {
			t.Error("expected idempotent reconciliation to produce the same certificate")
		}
	})
}
