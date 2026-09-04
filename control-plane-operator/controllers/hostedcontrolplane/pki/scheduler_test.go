package pki

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileSchedulerServerSecret(t *testing.T) {
	t.Parallel()

	t.Run("When secret is empty it should generate a valid cert", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ca := createTestCA(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scheduler-server",
				Namespace: "test-namespace",
			},
		}

		err := ReconcileSchedulerServerSecret(secret, ca, config.OwnerRef{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
		g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())
	})

	t.Run("When secret already has a valid cert it should not regenerate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ca := createTestCA(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scheduler-server",
				Namespace: "test-namespace",
			},
		}

		err := ReconcileSchedulerServerSecret(secret, ca, config.OwnerRef{})
		g.Expect(err).ToNot(HaveOccurred())

		initialCert := make([]byte, len(secret.Data[corev1.TLSCertKey]))
		copy(initialCert, secret.Data[corev1.TLSCertKey])

		err = ReconcileSchedulerServerSecret(secret, ca, config.OwnerRef{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(secret.Data[corev1.TLSCertKey]).To(Equal(initialCert))
	})
}

func createTestCA(t *testing.T) *corev1.Secret {
	t.Helper()
	g := NewWithT(t)

	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-ca",
			Namespace: "test-namespace",
		},
	}
	err := ReconcileRootCA(caSecret, config.OwnerRef{})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(caSecret.Data[certs.CASignerCertMapKey]).ToNot(BeEmpty())
	g.Expect(caSecret.Data[certs.CASignerKeyMapKey]).ToNot(BeEmpty())

	return caSecret
}
