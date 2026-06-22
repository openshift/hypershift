package k8sutil

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestEnsurePullSecret(t *testing.T) {
	t.Run("When secret is not present it should add it", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test"}}

		EnsurePullSecret(sa, "my-secret")
		g.Expect(sa.ImagePullSecrets).To(HaveLen(1))
		g.Expect(sa.ImagePullSecrets[0].Name).To(Equal("my-secret"))
	})

	t.Run("When secret is already present it should not duplicate", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "my-secret"},
			},
		}

		EnsurePullSecret(sa, "my-secret")
		g.Expect(sa.ImagePullSecrets).To(HaveLen(1))
	})

	t.Run("When adding a different secret it should append", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: "existing-secret"},
			},
		}

		EnsurePullSecret(sa, "new-secret")
		g.Expect(sa.ImagePullSecrets).To(HaveLen(2))
	})
}

type mockSAClient struct {
	sa       *corev1.ServiceAccount
	getErr   error
	token    *authenticationv1.TokenRequest
	tokenErr error
}

func (m *mockSAClient) Get(_ context.Context, _ string, _ metav1.GetOptions) (*corev1.ServiceAccount, error) {
	return m.sa, m.getErr
}

func (m *mockSAClient) CreateToken(_ context.Context, _ string, _ *authenticationv1.TokenRequest, _ metav1.CreateOptions) (*authenticationv1.TokenRequest, error) {
	return m.token, m.tokenErr
}

func TestCreateTokenForServiceAccount(t *testing.T) {
	t.Run("When service account is nil it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := CreateTokenForServiceAccount(t.Context(), nil, &mockSAClient{})
		g.Expect(err).To(MatchError(ContainSubstring("serviceaccount is nil")))
	})

	t.Run("When service account exists and token creation succeeds it should return the token", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test-sa", Namespace: "default"}}
		client := &mockSAClient{
			sa: sa,
			token: &authenticationv1.TokenRequest{
				Status: authenticationv1.TokenRequestStatus{Token: "my-token-value"},
			},
		}

		token, err := CreateTokenForServiceAccount(t.Context(), sa, client)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(token).To(Equal("my-token-value"))
	})

	t.Run("When service account is not found it should return a not-found error", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "missing-sa", Namespace: "default"}}
		client := &mockSAClient{
			getErr: apierrors.NewNotFound(schema.GroupResource{Resource: "serviceaccounts"}, "missing-sa"),
		}

		_, err := CreateTokenForServiceAccount(t.Context(), sa, client)
		g.Expect(err).To(MatchError(ContainSubstring("serviceaccount not found")))
	})

	t.Run("When Get returns a non-NotFound error it should propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test-sa", Namespace: "default"}}
		client := &mockSAClient{
			getErr: fmt.Errorf("connection refused"),
		}

		_, err := CreateTokenForServiceAccount(t.Context(), sa, client)
		g.Expect(err).To(MatchError(ContainSubstring("failed to get serviceaccount")))
		g.Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	t.Run("When token creation fails it should return a token error", func(t *testing.T) {
		g := NewWithT(t)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "test-sa", Namespace: "default"}}
		client := &mockSAClient{
			sa:       sa,
			tokenErr: fmt.Errorf("token request denied"),
		}

		_, err := CreateTokenForServiceAccount(t.Context(), sa, client)
		g.Expect(err).To(MatchError(ContainSubstring("failed to create token")))
		g.Expect(err).To(MatchError(ContainSubstring("token request denied")))
	})
}
