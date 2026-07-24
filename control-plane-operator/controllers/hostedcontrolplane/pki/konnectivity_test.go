package pki

import (
	"bytes"
	"crypto/x509"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// reconcileTestSigner creates a signer secret using the production reconcile function.
func reconcileTestSigner(t *testing.T, reconcile func(*corev1.Secret, config.OwnerRef) error) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	if err := reconcile(secret, config.OwnerRef{}); err != nil {
		t.Fatalf("failed to reconcile signer: %v", err)
	}
	return secret
}

func TestKonnectivitySeparateSigners(t *testing.T) {
	t.Parallel()

	// Exercise the production signer reconcile functions.
	serverServingSigner := reconcileTestSigner(t, ReconcileKonnectivityServerServingSignerSecret)
	clusterServingSigner := reconcileTestSigner(t, ReconcileKonnectivityClusterServingSignerSecret)
	serverAuthSigner := reconcileTestSigner(t, ReconcileKonnectivityServerAuthSignerSecret)
	clientAuthSigner := reconcileTestSigner(t, ReconcileKonnectivityClientAuthSignerSecret)
	legacySigner := reconcileTestSigner(t, ReconcileKonnectivitySignerSecret)

	ownerRef := config.OwnerRef{}

	t.Run("server cert uses server-serving signer", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-test"},
		}

		err := ReconcileKonnectivityServerSecret(secret, serverServingSigner, ownerRef)
		g.Expect(err).ToNot(HaveOccurred())

		cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))
		g.Expect(cert.Issuer.CommonName).To(Equal("konnectivity-server-serving-signer"))
	})

	t.Run("cluster cert uses cluster-serving signer", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-test"},
		}

		err := ReconcileKonnectivityClusterSecret(secret, clusterServingSigner, ownerRef, "konnectivity.example.com")
		g.Expect(err).ToNot(HaveOccurred())

		cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))
		g.Expect(cert.Issuer.CommonName).To(Equal("konnectivity-cluster-serving-signer"))
	})

	t.Run("client cert uses server-auth signer", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-test"},
		}

		err := ReconcileKonnectivityClientSecret(secret, serverAuthSigner, ownerRef)
		g.Expect(err).ToNot(HaveOccurred())

		cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageClientAuth))
		g.Expect(cert.Issuer.CommonName).To(Equal("konnectivity-server-auth-signer"))
	})

	t.Run("agent cert uses client-auth signer", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-test"},
		}

		err := ReconcileKonnectivityAgentSecret(secret, clientAuthSigner, ownerRef)
		g.Expect(err).ToNot(HaveOccurred())

		cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageClientAuth))
		g.Expect(cert.Issuer.CommonName).To(Equal("konnectivity-client-auth-signer"))
	})

	t.Run("all signers produce distinct CA certs", func(t *testing.T) {
		g := NewWithT(t)
		signers := []*corev1.Secret{serverServingSigner, clusterServingSigner, serverAuthSigner, clientAuthSigner, legacySigner}
		for i := range len(signers) {
			for j := i + 1; j < len(signers); j++ {
				g.Expect(bytes.Equal(
					signers[i].Data[certs.CASignerCertMapKey],
					signers[j].Data[certs.CASignerCertMapKey],
				)).To(BeFalse(), "signers %d and %d should have different CA certs", i, j)
			}
		}
	})

	t.Run("CA bundle aggregates all signers including legacy", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{}

		err := ReconcileKonnectivityConfigMap(cm, ownerRef,
			legacySigner, serverServingSigner, clusterServingSigner, serverAuthSigner, clientAuthSigner)
		g.Expect(err).ToNot(HaveOccurred())

		bundle := cm.Data[certs.CASignerCertMapKey]
		for _, signer := range []*corev1.Secret{legacySigner, serverServingSigner, clusterServingSigner, serverAuthSigner, clientAuthSigner} {
			g.Expect(bundle).To(ContainSubstring(string(signer.Data[certs.CASignerCertMapKey])))
		}
	})
}
