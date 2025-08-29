package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"
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

func (p *fakeIgnitionProvider) GetPayload(ctx context.Context, releaseImage, config, pullSecretHash, additionalTrustBundleHash, hcConfigurationHash string) (payload []byte, err error) {
	return []byte(fakePayload), nil
}

func TestReconcile(t *testing.T) {
	compressedConfig, err := util.CompressAndEncode([]byte("compressedConfig"))
	if err != nil {
		t.Fatal(err)
	}

	compressedConfigBytes := compressedConfig.Bytes()
	// badConfig contains data that the controller does not know how to decode and decompress.
	badConfig := []byte("bad config")

	testCases := []struct {
		name       string
		secret     client.Object
		validation func(t *testing.T, secret client.Object)
	}{
		{
			name: "When the payload can not be generated it should report message and reason in the token secret",
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
					TokenSecretConfigKey:  badConfig,
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
				g.Expect(err).To(HaveOccurred())

				// Get the secret.
				freshSecret := &corev1.Secret{}
				err = r.Client.Get(ctx, client.ObjectKeyFromObject(secret), freshSecret)
				g.Expect(err).ToNot(HaveOccurred())

				// Validate data for conditions
				g.Expect(freshSecret.Data[TokenSecretReasonKey]).To(BeEquivalentTo(InvalidConfigReason))
				g.Expect(freshSecret.Data[TokenSecretMessageKey]).To(BeEquivalentTo("failed to decode and decompress config: could not initialize gzip reader: illegal base64 data at input byte 3"))
			},
		},
		{
			name: "When a secret token ID is not cached it should be reconciled storing or deleting the payload",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						TokenSecretAnnotation: "true",
						// Use inplace upgrade type to test that the payload is compressed and encoded.
						TokenSecretNodePoolUpgradeType: string(hyperv1.UpgradeTypeInPlace),
					},
					CreationTimestamp: metav1.Now(),
				},
				Immutable: nil,
				Data: map[string][]byte{
					TokenSecretTokenKey:   []byte(uuid.New().String()),
					TokenSecretReleaseKey: []byte("release"),
					TokenSecretConfigKey:  compressedConfigBytes,
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

				// Validate data for conditions
				inplacePayload, err := util.DecodeAndDecompress(freshSecret.Data[TokenSecretPayloadKey])
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(inplacePayload.String()).To(BeEquivalentTo(fakePayload))
				g.Expect(freshSecret.Data[TokenSecretReasonKey]).To(BeEquivalentTo(hyperv1.AsExpectedReason))
				g.Expect(freshSecret.Data[TokenSecretMessageKey]).To(BeEquivalentTo("Payload generated successfully"))

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
					TokenSecretConfigKey:  compressedConfigBytes,
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
		{
			name: "When the nodepool upgrade strategy is replace, the token secret should not contain the machine payload",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						TokenSecretAnnotation:          "true",
						TokenSecretNodePoolUpgradeType: string(hyperv1.UpgradeTypeReplace),
					},
					CreationTimestamp: metav1.Now(),
				},
				Immutable: nil,
				Data: map[string][]byte{
					TokenSecretTokenKey:   []byte(uuid.New().String()),
					TokenSecretReleaseKey: []byte("release"),
					TokenSecretConfigKey:  compressedConfigBytes,
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

				// Validate that the payload was not stored in the token secret
				g.Expect(freshSecret.Data[TokenSecretPayloadKey]).To(BeEmpty())
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

func TestIsTokenExpired(t *testing.T) {
	testCases := []struct {
		name              string
		annotations       map[string]string
		expectedIsExpired bool
	}{
		{
			name:              "when there's no token expiration timestamp annotation it should return that it is not expired (false)",
			annotations:       map[string]string{},
			expectedIsExpired: false,
		},
		{
			name: "when the token expiration timestamp is in the past it should return that it is expired (true)",
			annotations: map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: time.Now().Add(-4 * time.Hour).Format(time.RFC3339),
			},
			expectedIsExpired: true,
		},
		{
			name: "when the token expiration timestamp is in the future it should return that it is not expired (false)",
			annotations: map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: time.Now().Add(4 * time.Hour).Format(time.RFC3339),
			},
			expectedIsExpired: false,
		},
		{
			name: "when the token expiration timestamp has an invalid value it should return that it is expired (true)",
			annotations: map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: "badvalue",
			},
			expectedIsExpired: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			actualIsExpired := isTokenExpired(logr.Discard(), tc.annotations)
			g.Expect(actualIsExpired).To(Equal(tc.expectedIsExpired))
		})
	}
}

func TestProcessedExpiredToken(t *testing.T) {
	fakeName := "test-token"
	fakeNamespace := "master-cluster1"
	fakeCurrentTokenVal := "tokenval1"
	fakeOldTokenVal := "oldtokenval2"
	fakeIndependentTokenVal := "independenttokenval1"
	fakeTokenContent := []byte(`blah`)

	testCases := []struct {
		name                       string
		inputSecret                *corev1.Secret
		inputEntries               map[string][]byte
		expectedRemainingEntries   map[string][]byte
		expectedEntriesToBeRemoved map[string][]byte
	}{
		{
			name: "when a token secret exists and the cache is populated then the secret is deleted and the token entries removed from cache",
			inputEntries: map[string][]byte{
				fakeCurrentTokenVal: fakeTokenContent,
				fakeOldTokenVal:     fakeTokenContent,
			},
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
				Data: map[string][]byte{
					TokenSecretOldTokenKey: []byte(fakeOldTokenVal),
					TokenSecretTokenKey:    []byte(fakeCurrentTokenVal),
				},
			},
			expectedRemainingEntries: nil,
			expectedEntriesToBeRemoved: map[string][]byte{
				fakeCurrentTokenVal: fakeTokenContent,
				fakeOldTokenVal:     fakeTokenContent,
			},
		},
		{
			name: "when a token secret exists with only one token and the cache is populated then the secret is deleted and the token entries removed from cache",
			inputEntries: map[string][]byte{
				fakeCurrentTokenVal: fakeTokenContent,
			},
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
				Data: map[string][]byte{
					TokenSecretTokenKey: []byte(fakeCurrentTokenVal),
				},
			},
			expectedRemainingEntries: nil,
			expectedEntriesToBeRemoved: map[string][]byte{
				fakeCurrentTokenVal: fakeTokenContent,
			},
		},
		{
			name: "when a token secret exists and an independent secrets entry is also in the cache then only the processed tokens are removed",
			inputEntries: map[string][]byte{
				fakeCurrentTokenVal:     fakeTokenContent,
				fakeIndependentTokenVal: fakeTokenContent,
			},
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
				Data: map[string][]byte{
					TokenSecretTokenKey: []byte(fakeCurrentTokenVal),
				},
			},
			expectedRemainingEntries: map[string][]byte{
				fakeIndependentTokenVal: fakeTokenContent,
			},
			expectedEntriesToBeRemoved: map[string][]byte{
				fakeCurrentTokenVal: fakeTokenContent,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			payloadStore := NewPayloadStore()
			for tokenKey, tokenVal := range tc.inputEntries {
				payloadStore.Set(tokenKey, CacheValue{
					Payload: tokenVal,
				})
			}
			r := TokenSecretReconciler{
				Client:           fake.NewClientBuilder().WithObjects(tc.inputSecret).Build(),
				IgnitionProvider: &fakeIgnitionProvider{},
				PayloadStore:     payloadStore,
			}
			err := r.processExpiredToken(context.Background(), tc.inputSecret)
			g.Expect(err).To(Not(HaveOccurred()))
			for expectedTokenKey := range tc.expectedRemainingEntries {
				_, ok := payloadStore.Get(expectedTokenKey)
				g.Expect(ok).To(BeTrue())
			}
			for expectedTokenKey := range tc.expectedEntriesToBeRemoved {
				_, ok := payloadStore.Get(expectedTokenKey)
				g.Expect(ok).To(BeFalse())
			}
			secretToFetch := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
			}
			err = r.Client.Get(context.Background(), client.ObjectKeyFromObject(secretToFetch), secretToFetch)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	}
}

func TestHasSameReasonAndMessage(t *testing.T) {
	testCases := []struct {
		name     string
		secret   *corev1.Secret
		reason   string
		message  error
		expected bool
	}{
		{
			name: "Reason and message match",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretReasonKey:  []byte("reason1"),
					TokenSecretMessageKey: []byte("message1"),
				},
			},
			reason:   "reason1",
			message:  fmt.Errorf("message1"),
			expected: true,
		},
		{
			name: "Reason does not match",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretReasonKey:  []byte("reason1"),
					TokenSecretMessageKey: []byte("message1"),
				},
			},
			reason:   "reason2",
			message:  fmt.Errorf("message1"),
			expected: false,
		},
		{
			name: "Message does not match",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretReasonKey:  []byte("reason1"),
					TokenSecretMessageKey: []byte("message1"),
				},
			},
			reason:   "reason1",
			message:  fmt.Errorf("message2"),
			expected: false,
		},
		{
			name: "Both reason and message do not match",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretReasonKey:  []byte("reason1"),
					TokenSecretMessageKey: []byte("message1"),
				},
			},
			reason:   "reason2",
			message:  fmt.Errorf("message2"),
			expected: false,
		},
		{
			name: "Reason and message are empty",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretReasonKey:  []byte(""),
					TokenSecretMessageKey: []byte(""),
				},
			},
			reason:   "",
			message:  fmt.Errorf(""),
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result := hasSameReasonAndMessage(tc.secret, tc.reason, tc.message)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
