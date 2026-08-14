package pki

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileEtcdPeerSecret(t *testing.T) {
	t.Parallel()

	caCfg := certs.CertCfg{
		IsCA:    true,
		Subject: pkix.Name{CommonName: "etcd-signer", OrganizationalUnit: []string{"openshift"}},
	}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}
	caSecret := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: certs.CertToPem(caCert),
			certs.CASignerKeyMapKey:  certs.PrivateKeyToPem(caKey),
		},
	}

	t.Run("When reconciling etcd peer secret it should place DNS names and IPs in correct cert fields", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "clusters-test",
			},
		}

		err := ReconcileEtcdPeerSecret(secret, caSecret, config.OwnerRef{})
		g.Expect(err).ToNot(HaveOccurred())

		certData := secret.Data[EtcdPeerCrtKey]
		g.Expect(certData).ToNot(BeEmpty())

		cert, err := certs.PemToCertificate(certData)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(cert.DNSNames).To(ContainElements(
			"*.etcd-discovery.clusters-test.svc",
			"*.etcd-discovery.clusters-test.svc.cluster.local",
		))

		g.Expect(cert.IPAddresses).To(ContainElements(
			net.IPv4(127, 0, 0, 1).To4(),
			net.ParseIP("::1"),
		))
		g.Expect(cert.DNSNames).ToNot(ContainElement(ContainSubstring("etcd-client")))
		g.Expect(cert.DNSNames).ToNot(ContainElement("127.0.0.1"))
		g.Expect(cert.DNSNames).ToNot(ContainElement("::1"))
	})

	t.Run("When reconciling etcd peer secret it should have client and server auth usage", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "clusters-test",
			},
		}

		err := ReconcileEtcdPeerSecret(secret, caSecret, config.OwnerRef{})
		g.Expect(err).ToNot(HaveOccurred())

		cert, err := certs.PemToCertificate(secret.Data[EtcdPeerCrtKey])
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(cert.ExtKeyUsage).To(ContainElements(
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		))
	})
}
