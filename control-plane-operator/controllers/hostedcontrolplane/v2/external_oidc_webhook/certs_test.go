package extoidc

import (
	"context"
	"crypto/x509"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptCACertSecret(t *testing.T) {
	t.Run("When certificate signing is enabled, it should generate a TLS certificate authority", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{}

		g.Expect(adaptCACertSecret(newCertsWorkloadContext(t, false), secret)).To(Succeed())
		g.Expect(secret.Type).To(Equal(corev1.SecretTypeTLS))
		g.Expect(secret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(secret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))

		certificate, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certificate.IsCA).To(BeTrue())
		g.Expect(certificate.Subject.CommonName).To(Equal(caName))
		g.Expect(certificate.Subject.OrganizationalUnit).To(ConsistOf("openshift"))
	})

	t.Run("When certificate signing is skipped, it should leave the secret unchanged", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"existing": []byte("value")},
		}
		expected := secret.DeepCopy()

		g.Expect(adaptCACertSecret(newCertsWorkloadContext(t, true), secret)).To(Succeed())
		g.Expect(secret).To(Equal(expected))
	})
}

func TestAdaptServingCertSecret(t *testing.T) {
	t.Run("When the certificate authority is not reconciled yet, it should leave the serving secret unchanged", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{Data: map[string][]byte{"existing": []byte("value")}}
		expected := secret.DeepCopy()

		g.Expect(adaptServingCertSecret(newCertsWorkloadContext(t, false), secret)).To(Succeed())
		g.Expect(secret).To(Equal(expected))
	})

	t.Run("When the certificate authority exists, it should generate a serving certificate for the webhook service", func(t *testing.T) {
		g := NewWithT(t)
		caSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: caName, Namespace: "test-ns"}}
		g.Expect(adaptCACertSecret(newCertsWorkloadContext(t, false), caSecret)).To(Succeed())

		servingSecret := &corev1.Secret{}
		g.Expect(adaptServingCertSecret(newCertsWorkloadContext(t, false, caSecret), servingSecret)).To(Succeed())
		g.Expect(servingSecret.Type).To(Equal(corev1.SecretTypeTLS))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))

		certificate, err := certs.PemToCertificate(servingSecret.Data[corev1.TLSCertKey])
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(certificate.Subject.CommonName).To(Equal(ComponentName))
		g.Expect(certificate.Subject.Organization).To(ConsistOf("openshift"))
		g.Expect(certificate.DNSNames).To(ConsistOf(
			ComponentName,
			ComponentName+".test-ns.svc",
			ComponentName+".test-ns.svc.cluster.local",
		))

		caCertificate, err := certs.PemToCertificate(caSecret.Data[corev1.TLSCertKey])
		g.Expect(err).NotTo(HaveOccurred())
		roots := x509.NewCertPool()
		roots.AddCert(caCertificate)
		_, err = certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: ComponentName})
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("When certificate signing is skipped, it should leave the serving secret unchanged", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"existing": []byte("value")},
		}
		expected := secret.DeepCopy()

		g.Expect(adaptServingCertSecret(newCertsWorkloadContext(t, true), secret)).To(Succeed())
		g.Expect(secret).To(Equal(expected))
	})
}

func newCertsWorkloadContext(t *testing.T, skipCertificateSigning bool, objects ...client.Object) component.WorkloadContext {
	t.Helper()

	return component.WorkloadContext{
		Context:                context.Background(),
		Client:                 fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
		SkipCertificateSigning: skipCertificateSigning,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
		},
	}
}
