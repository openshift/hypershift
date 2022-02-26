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
			name: "When a secret token ID is not cached it should be reconciled storing or deleting the payload",
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
				freshSecret := &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate that the tokenID was not rotated.
				originalSecret, _ := secret.(*corev1.Secret)
				tokenID := freshSecret.Data[TokenSecretTokenKey]
				g.Expect(originalSecret.Data[TokenSecretTokenKey]).To(BeEquivalentTo(tokenID))
				g.Expect(freshSecret.Data).ToNot(HaveKey(TokenSecretOldTokenKey))

				// Validate that payload was stored in the cache.
				value, found := r.PayloadStore.Get(string(tokenID))
				g.Expect(found).To(BeTrue())
				g.Expect(value.Payload).To(BeEquivalentTo(fakePayload))
				g.Expect(value.SecretName).To(BeEquivalentTo(secret.GetName()))

				// Reconcile here to validate that when a token is cached and has no TokenSecretTokenGenerationTime it should be rotated.
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Validate the token ID was rotated.
				freshSecret = &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(freshSecret.Data[TokenSecretTokenKey]).ToNot(BeEquivalentTo(originalSecret.Data[TokenSecretTokenKey]))
				g.Expect(freshSecret.Data[TokenSecretOldTokenKey]).To(BeEquivalentTo(originalSecret.Data[TokenSecretTokenKey]))
				// Validate a TokenSecretTokenGenerationTime was added.
				g.Expect(freshSecret.Annotations[TokenSecretTokenGenerationTime]).ToNot(BeEmpty())

				// Delete the secret.
				err = r.Client.Delete(ctx, secret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate the secret is really gone.
				freshSecret = &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).To(HaveOccurred())
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected notFound error, got: %v", err)
				}

				// Reconcile here should delete the payload from the cache.
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Validate that payload was deleted from the cache.
				value, found = r.PayloadStore.Get(string(tokenID))
				g.Expect(found).To(BeFalse())
				g.Expect(value.Payload).To(BeEquivalentTo(""))

				value, found = r.PayloadStore.Get(string(freshSecret.Data[TokenSecretOldTokenKey]))
				g.Expect(found).To(BeFalse())
				g.Expect(value.Payload).To(BeEquivalentTo(""))
			},
		},
		{
			name: "When a secret token ID has lived beyond 1/2 ttl it should be rotated",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						TokenSecretAnnotation:          "true",
						TokenSecretTokenGenerationTime: metav1.Now().Add(-ttl / 2).Format(time.RFC3339Nano),
					},
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

				now := time.Now()
				// Get the secret.
				freshSecret := &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).ToNot(HaveOccurred())
				oldToken := freshSecret.Data[TokenSecretTokenKey]

				// Manually set an expired token and the old token in the cache.
				expiredTokenID := "expired"
				r.PayloadStore.RLock()
				r.PayloadStore.cache[expiredTokenID] = &entry{
					value: CacheValue{
						Payload:    []byte(fakePayload),
						SecretName: secret.GetName(),
					},
					expiry: now.Add(-1 * time.Hour),
				}
				r.PayloadStore.cache[string(oldToken)] = &entry{
					value: CacheValue{
						Payload:    []byte(fakePayload),
						SecretName: secret.GetName(),
					},
					expiry: now.Add(ttl / 2),
				}
				r.PayloadStore.RUnlock()

				// Reconcile here should rotate the tokenID, keep the existing one in the cache and delete the expired one.
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
				g.Expect(err).ToNot(HaveOccurred())

				// Get the fresh Secret.
				freshSecret = &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate that the expired tokenID was deleted from the cache.
				value, found := r.PayloadStore.Get(expiredTokenID)
				g.Expect(found).To(BeFalse())
				g.Expect(value.Payload).To(BeEquivalentTo(""))

				// Validate that the old tokenID still exists in the cache.
				value, found = r.PayloadStore.Get(string(oldToken))
				g.Expect(found).To(BeTrue())
				g.Expect(value.Payload).To(BeEquivalentTo(fakePayload))
				g.Expect(r.PayloadStore.cache[string(oldToken)].expiry).To(BeEquivalentTo(now.Add(ttl / 2)))
				g.Expect(freshSecret.Data[TokenSecretOldTokenKey]).To(BeEquivalentTo(oldToken))

				// Validate that the new tokenID was persisted in the cache.
				newToken := freshSecret.Data[TokenSecretTokenKey]
				g.Expect(newToken).ToNot(BeEquivalentTo(oldToken))

				value, found = r.PayloadStore.Get(string(newToken))
				g.Expect(found).To(BeTrue())
				g.Expect(value.Payload).To(BeEquivalentTo(fakePayload))
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

func TestGetTokenIDTimeLived(t *testing.T) {
	now := time.Now()
	lastUpdated := now.Add(-time.Hour).Format(time.RFC3339Nano)
	expectedDuration := time.Hour

	testCases := []struct {
		name             string
		annotations      map[string]string
		expectedDuration *time.Duration
		expectedError    bool
	}{
		{
			name:             "when there's no annotation it should return nil",
			annotations:      map[string]string{},
			expectedDuration: nil,
			expectedError:    false,
		},
		{
			name: "when the annotation has empty value it should error",
			annotations: map[string]string{
				TokenSecretTokenGenerationTime: "",
			},
			expectedDuration: nil,
			expectedError:    true,
		},
		{
			name: "when the annotation has no wrong format it should error",
			annotations: map[string]string{
				TokenSecretTokenGenerationTime: "wrong format",
			},
			expectedDuration: nil,
			expectedError:    true,
		},
		{
			name: "when the annotation has a valid format it should return a duration",
			annotations: map[string]string{
				TokenSecretTokenGenerationTime: lastUpdated,
			},
			expectedDuration: &expectedDuration,
			expectedError:    false,
		},
	}

	for _, tc := range testCases {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: tc.annotations,
			},
		}
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			timeLived, err := getTokenTimeLived(secret, now)
			g.Expect(err != nil).To(BeEquivalentTo(tc.expectedError))
			g.Expect(timeLived).To(BeEquivalentTo(tc.expectedDuration))
		})
	}
}

func TestTokenIDNeedRotation(t *testing.T) {
	timeLivedHalfTTL := time.Duration(ttl / 2)
	timeLivedLessThanTTL := time.Duration(ttl/2 - 1)
	testCases := []struct {
		name         string
		timeLived    *time.Duration
		needRotation bool
	}{
		{
			name:         "when the time lived is >= ttl it should return true",
			timeLived:    &timeLivedHalfTTL,
			needRotation: true,
		},
		{
			name:         "when the time lived is nil it should return true",
			timeLived:    nil,
			needRotation: true,
		},
		{
			name:         "when the time lived is < ttl it should return true",
			timeLived:    &timeLivedLessThanTTL,
			needRotation: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			needRotation := tokenNeedRotation(tc.timeLived)
			g.Expect(needRotation).To(BeEquivalentTo(tc.needRotation))
		})
	}
}

func TestRotateTokenID(t *testing.T) {
	g := NewWithT(t)

	oldToken := []byte("old")
	secretName := "test"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
		Data: map[string][]byte{
			TokenSecretTokenKey: oldToken,
		},
	}

	existingValue := CacheValue{
		Payload:    []byte("fake"),
		SecretName: secretName,
	}
	r := TokenSecretReconciler{
		Client:           fake.NewClientBuilder().WithObjects(secret).Build(),
		IgnitionProvider: &fakeIgnitionProvider{},
		PayloadStore:     NewPayloadStore(),
	}

	err := r.rotateToken(context.Background(), secret, existingValue, time.Now())
	g.Expect(err).ToNot(HaveOccurred())

	freshSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
		},
	}
	err = r.Get(context.Background(), client.ObjectKeyFromObject(freshSecret), freshSecret)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(freshSecret.Annotations[TokenSecretTokenGenerationTime]).ToNot(BeEmpty())
	newToken := freshSecret.Data[TokenSecretTokenKey]
	g.Expect(newToken).ToNot(BeEmpty())
	g.Expect(newToken).ToNot(BeEquivalentTo(oldToken))
	g.Expect(freshSecret.Data[TokenSecretOldTokenKey]).To(BeEquivalentTo(oldToken))

	value, ok := r.PayloadStore.Get(string(newToken))
	g.Expect(value).To(BeEquivalentTo(existingValue))
	g.Expect(ok).To(BeTrue())
}
