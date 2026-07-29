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

const (
	csiSnapshotControllerOperatorServingCertSecretName = "csi-snapshot-controller-operator-serving-cert"
	csiSnapshotControllerOperatorHostname              = "csi-snapshot-controller-operator"
)

func TestReconcileCSISnapshotControllerOperatorServingCert(t *testing.T) {
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

	ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca",
			Namespace: namespace,
		},
	}
	if err := reconcileSelfSignedCA(ca, ownerRef, "test-org", "test-ca"); err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}

	t.Run("When reconciling with a valid CA, it should generate a cert with correct DNS names and subject", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      csiSnapshotControllerOperatorServingCertSecretName,
				Namespace: namespace,
			},
		}

		if err := ReconcileCSISnapshotControllerOperatorServingCert(secret, ca, ownerRef); err != nil {
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

		expectedDNSNames := map[string]bool{
			csiSnapshotControllerOperatorHostname: true,
			"localhost":                           true,
		}

		if len(cert.DNSNames) != len(expectedDNSNames) {
			t.Errorf("expected %d DNS names, got %d", len(expectedDNSNames), len(cert.DNSNames))
		}

		for _, name := range cert.DNSNames {
			if !expectedDNSNames[name] {
				t.Errorf("unexpected DNS name: %s", name)
			}
		}

		expectedCN := csiSnapshotControllerOperatorHostname
		if cert.Subject.CommonName != expectedCN {
			t.Errorf("expected CN %s, got %s", expectedCN, cert.Subject.CommonName)
		}

		if len(cert.Subject.Organization) == 0 || cert.Subject.Organization[0] != "openshift" {
			t.Errorf("expected Organization [openshift], got %v", cert.Subject.Organization)
		}
	})

	t.Run("When reconciling the serving cert twice, it should produce the same certificate", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      csiSnapshotControllerOperatorServingCertSecretName,
				Namespace: namespace,
			},
		}

		if err := ReconcileCSISnapshotControllerOperatorServingCert(secret, ca, ownerRef); err != nil {
			t.Fatalf("first reconcile failed: %v", err)
		}

		firstCert := make([]byte, len(secret.Data[corev1.TLSCertKey]))
		copy(firstCert, secret.Data[corev1.TLSCertKey])

		if err := ReconcileCSISnapshotControllerOperatorServingCert(secret, ca, ownerRef); err != nil {
			t.Fatalf("second reconcile failed: %v", err)
		}

		if string(firstCert) != string(secret.Data[corev1.TLSCertKey]) {
			t.Error("expected idempotent reconciliation to produce the same certificate")
		}
	})
}
