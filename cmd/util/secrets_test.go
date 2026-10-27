package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetSecretWithClient(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "clusters"},
	}).Build()

	t.Run("When the secret exists, it should return the secret", func(t *testing.T) {
		secret, err := GetSecretWithClient(t.Context(), client, "credentials", "clusters")
		NewWithT(t).Expect(err).NotTo(HaveOccurred())
		NewWithT(t).Expect(secret.Name).To(Equal("credentials"))
	})
	t.Run("When the client is nil, it should return an error", func(t *testing.T) {
		_, err := GetSecretWithClient(t.Context(), nil, "credentials", "clusters")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
}

func TestExtractOptionsFromSecret(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: "clusters"},
		Data: map[string][]byte{
			"baseDomain":            []byte("example.com"),
			"aws_access_key_id":     []byte("access-key"),
			"aws_secret_access_key": []byte("secret-key"),
			"aws_session_token":     []byte("session-token"),
		},
	}).Build()

	data, err := ExtractOptionsFromSecret(t.Context(), client, "credentials", "clusters", "")
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
	NewWithT(t).Expect(data.BaseDomain).To(Equal("example.com"))
	NewWithT(t).Expect(data.AWSAccessKeyID).To(Equal("access-key"))
}
