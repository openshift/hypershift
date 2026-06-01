package kubeadminpassword

import (
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"golang.org/x/crypto/bcrypt"
)

func TestReconcileKubeadminPasswordHashSecret(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		existingHash  []byte
		password      []byte
		expectNewHash bool
	}{
		"When no existing hash it should generate a new hash": {
			password:      []byte("adminpass"),
			expectNewHash: true,
		},
		"When existing hash does not match the password it should regenerate the hash": {
			existingHash:  []byte("stale-non-matching-hash"),
			password:      []byte("adminpass"),
			expectNewHash: true,
		},
		"When existing hash is a valid bcrypt hash of a different password it should regenerate the hash": {
			existingHash: func() []byte {
				h, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
				return h
			}(),
			password:      []byte("adminpass"),
			expectNewHash: true,
		},
		"When hash already matches password it should not regenerate": {
			existingHash: func() []byte {
				h, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.MinCost)
				return h
			}(),
			password:      []byte("adminpass"),
			expectNewHash: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubeadmin-password-hash",
					Namespace: "kube-system",
				},
			}
			if test.existingHash != nil {
				secret.Data = map[string][]byte{"kubeadmin": test.existingHash}
			}
			passwordSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubeadmin-password",
					Namespace: "master-cluster1",
				},
				Data: map[string][]byte{"password": test.password},
			}

			err := ReconcileKubeadminPasswordHashSecret(secret, passwordSecret)
			g.Expect(err).To(BeNil())
			g.Expect(secret.Data["kubeadmin"]).ToNot(BeEmpty())
			g.Expect(bcrypt.CompareHashAndPassword(secret.Data["kubeadmin"], test.password)).To(BeNil())

			if !test.expectNewHash {
				g.Expect(secret.Data["kubeadmin"]).To(Equal(test.existingHash))
			}
		})
	}
}
