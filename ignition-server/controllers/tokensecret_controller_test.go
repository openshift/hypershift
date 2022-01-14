package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	fakePayload = "test"
)

type fakeIgnitionProvider struct{}

func (p *fakeIgnitionProvider) GetPayload(ctx context.Context, releaseImage string, config string) (payload []byte, err error) {
	return []byte(fakePayload), nil
}

func TestReconcile(t *testing.T) {
	compressedConfig, err := compress([]byte("compressedConfig"))
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name       string
		secret     client.Object
		validation func(t *testing.T, secret client.Object)
	}{
		{
			name: "When a secret is non expired it reconciles it storing or deleting the payload",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						TokenSecretAnnotation: "true",
					},
					CreationTimestamp: metav1.Now(),
				},
				Immutable: nil,
				Data: map[string][]byte{
					TokenSecretTokenKey:   []byte(uuid.New().String()),
					TokenSecretReleaseKey: []byte("release"),
					TokenSecretConfigKey:  compressedConfig,
				},
			},
			validation: func(t *testing.T, secret client.Object) {
				ctx := context.Background()
				r := TokenSecretReconciler{
					Client:           fake.NewClientBuilder().WithObjects(secret).Build(),
					IgnitionProvider: &fakeIgnitionProvider{},
					PayloadStore:     NewPayloadStore(),
				}
				g := NewWithT(t)
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Get the secret.
				gotSecret := &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), gotSecret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate that payload was stored in the cache.
				token := gotSecret.Data[TokenSecretTokenKey]
				value, found := r.PayloadStore.Get(string(token))
				g.Expect(found).To(BeTrue())
				g.Expect(value.Payload).To(BeEquivalentTo(fakePayload))
				g.Expect(value.SecretName).To(BeEquivalentTo(secret.GetName()))

				// Delete the secret.
				err = r.Client.Delete(ctx, secret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate the secret is really gone.
				gotSecret = &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), gotSecret)
				g.Expect(err).To(HaveOccurred())
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected notFound error, got: %v", err)
				}

				// Reconcile here should delete the payload from the cache.
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Validate that payload was deleted from the cache.
				value, found = r.PayloadStore.Get(string(token))
				g.Expect(found).To(BeFalse())
				g.Expect(value.Payload).To(BeEquivalentTo(""))
			},
		},
		{
			name: "When a secret is expired it deletes it",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						TokenSecretAnnotation: "true",
					},
					CreationTimestamp: metav1.NewTime(metav1.Now().Add(-ttl - 1*time.Hour)),
				},
				Immutable: nil,
				Data: map[string][]byte{
					TokenSecretTokenKey:   []byte(uuid.New().String()),
					TokenSecretReleaseKey: []byte("release"),
					TokenSecretConfigKey:  compressedConfig,
				},
			},
			validation: func(t *testing.T, secret client.Object) {
				ctx := context.Background()
				r := TokenSecretReconciler{
					Client:           fake.NewClientBuilder().WithObjects(secret).Build(),
					IgnitionProvider: &fakeIgnitionProvider{},
					PayloadStore:     NewPayloadStore(),
				}
				g := NewWithT(t)

				// Get the secret.
				gotSecret := &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), gotSecret)
				g.Expect(err).ToNot(HaveOccurred())

				// Manually set the token in the cache.
				token := gotSecret.Data[TokenSecretTokenKey]
				r.PayloadStore.Set(string(token), CacheValue{SecretName: secret.GetName(), Payload: []byte(fakePayload)})
				value, found := r.PayloadStore.Get(string(token))
				g.Expect(found).To(BeTrue())
				g.Expect(value.Payload).To(BeEquivalentTo(fakePayload))
				g.Expect(value.SecretName).To(BeEquivalentTo(secret.GetName()))

				// Reconcile here should delete the payload from the cache.
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Validate the secret is really deleted.
				gotSecret = &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), gotSecret)
				g.Expect(err).To(HaveOccurred())
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected notFound error, got: %v", err)
				}

				// Validate that payload was deleted from the cache.
				token = gotSecret.Data[TokenSecretTokenKey]
				value, found = r.PayloadStore.Get(string(token))
				g.Expect(found).To(BeFalse())
				g.Expect(value.Payload).To(BeEquivalentTo(""))
			},
		},
	}

	// Set the logger so the tested funcs log accordingly.
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.validation(t, tc.secret)
		})
	}
}
