package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func hostedClusterWithDefaultCert(name string) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters",
		},
		Spec: hyperv1.HostedClusterSpec{
			OperatorConfiguration: &hyperv1.OperatorConfiguration{
				IngressOperator: &hyperv1.IngressOperatorSpec{
					DefaultCertificate: hyperv1.IngressDefaultCertificateReference{
						Name: name,
					},
				},
			},
		},
	}
}

func TestReconcileIngressDefaultCertSync(t *testing.T) {
	tests := []struct {
		name           string
		hcluster       *hyperv1.HostedCluster
		existingSecret *corev1.Secret
		// staleDestSecret seeds a previously synced secret in the control plane
		// namespace to verify it is cleaned up when the feature is no longer in use.
		staleDestSecret bool
		// failGetSecret makes the source secret Get return a non-NotFound error to
		// exercise the transient-failure path.
		failGetSecret bool
		// expectError is only for unexpected/transient failures that should retry.
		expectError bool
		// expectSync asserts the destination secret was written.
		expectSync bool
		// expectCondition is the expected status of the IngressDefaultCertificateSynced
		// condition. Empty means the condition must be absent.
		expectCondition metav1.ConditionStatus
		expectReason    string
	}{
		{
			name: "When DefaultCertificate is not set, it should be a no-op with no condition",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			expectSync: false,
		},
		{
			name: "When OperatorConfiguration is nil, it should be a no-op with no condition",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: nil,
				},
			},
			expectSync: false,
		},
		{
			name:     "When DefaultCertificate is set with valid TLS secret, it should sync and set the condition True",
			hcluster: hostedClusterWithDefaultCert("my-tls-cert"),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-tls-cert",
					Namespace: "clusters",
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("cert-data"),
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			expectSync:      true,
			expectCondition: metav1.ConditionTrue,
			expectReason:    hyperv1.AsExpectedReason,
		},
		{
			name: "When the ingress capability is disabled, it should skip the sync and set no condition",
			hcluster: func() *hyperv1.HostedCluster {
				hc := hostedClusterWithDefaultCert("my-tls-cert")
				hc.Spec.Capabilities = &hyperv1.Capabilities{
					Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability},
				}
				return hc
			}(),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-tls-cert",
					Namespace: "clusters",
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("cert-data"),
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			expectSync: false,
		},
		{
			name: "When platform is IBM Cloud, it should skip the sync and set the condition False",
			hcluster: func() *hyperv1.HostedCluster {
				hc := hostedClusterWithDefaultCert("my-tls-cert")
				hc.Spec.Platform.Type = hyperv1.IBMCloudPlatform
				return hc
			}(),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-tls-cert",
					Namespace: "clusters",
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("cert-data"),
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			expectSync:      false,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.IngressDefaultCertificatePlatformNotSupportedReason,
		},
		{
			name:     "When source secret is missing tls.crt, it should not fail and set the condition False",
			hcluster: hostedClusterWithDefaultCert("bad-cert"),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-cert",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			expectSync:      false,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.IngressDefaultCertificateInvalidReason,
		},
		{
			name:     "When source secret is missing tls.key, it should not fail and set the condition False",
			hcluster: hostedClusterWithDefaultCert("bad-cert"),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-cert",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey: []byte("cert-data"),
				},
			},
			expectSync:      false,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.IngressDefaultCertificateInvalidReason,
		},
		{
			name:            "When source secret does not exist, it should not fail and set the condition False",
			hcluster:        hostedClusterWithDefaultCert("nonexistent-cert"),
			expectSync:      false,
			expectCondition: metav1.ConditionFalse,
			expectReason:    hyperv1.SecretNotFoundReason,
		},
		{
			name:          "When getting the source secret fails unexpectedly, it should return an error",
			hcluster:      hostedClusterWithDefaultCert("boom-cert"),
			failGetSecret: true,
			expectError:   true,
		},
		{
			name: "When DefaultCertificate is cleared, the stale condition and synced secret should be removed",
			hcluster: func() *hyperv1.HostedCluster {
				hc := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "clusters",
					},
				}
				meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.IngressDefaultCertificateSynced),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				})
				return hc
			}(),
			staleDestSecret: true,
			expectSync:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctx := context.Background()

			controlPlaneNamespace := "clusters-test-cluster"
			objs := []client.Object{tt.hcluster}
			if tt.existingSecret != nil {
				objs = append(objs, tt.existingSecret)
			}
			if tt.staleDestSecret {
				stale := cpomanifests.ServiceProviderDefaultIngressServingCert(controlPlaneNamespace)
				stale.Type = corev1.SecretTypeTLS
				stale.Data = map[string][]byte{
					corev1.TLSCertKey:       []byte("stale-cert"),
					corev1.TLSPrivateKeyKey: []byte("stale-key"),
				}
				objs = append(objs, stale)
			}

			builder := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(tt.hcluster)
			if tt.failGetSecret {
				secretName := tt.hcluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.Secret); ok && key.Name == secretName {
							return apierrors.NewInternalError(fmt.Errorf("simulated get failure"))
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			}
			fakeClient := builder.Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
				now:    metav1.Now,
			}

			// Guard against a vacuous pass: when a case pre-seeds the condition and
			// then expects it removed, confirm the fake client actually persisted it
			// before reconciliation so the case proves removal.
			if tt.expectCondition == "" && meta.FindStatusCondition(tt.hcluster.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced)) != nil {
				seeded := &hyperv1.HostedCluster{}
				g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.hcluster), seeded)).To(Succeed())
				g.Expect(meta.FindStatusCondition(seeded.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))).ToNot(BeNil(),
					"pre-seeded condition should be persisted before reconciliation")
			}

			err := r.reconcileIngressDefaultCertSync(ctx, tt.hcluster, upsert.New(false).CreateOrUpdate, controlPlaneNamespace)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			expected := cpomanifests.ServiceProviderDefaultIngressServingCert(controlPlaneNamespace)
			if tt.expectSync {
				dest := &corev1.Secret{}
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(expected), dest)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(dest.Type).To(Equal(corev1.SecretTypeTLS))
				g.Expect(dest.Data[corev1.TLSCertKey]).To(Equal(tt.existingSecret.Data[corev1.TLSCertKey]))
				g.Expect(dest.Data[corev1.TLSPrivateKeyKey]).To(Equal(tt.existingSecret.Data[corev1.TLSPrivateKeyKey]))
			} else {
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(expected), &corev1.Secret{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "destination secret should not be created when the sync is skipped")
			}

			// Read back the persisted status to assert on the condition.
			updated := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.hcluster), updated)).To(Succeed())
			condition := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))
			if tt.expectCondition == "" {
				g.Expect(condition).To(BeNil(), "IngressDefaultCertificateSynced condition should be absent")
				return
			}
			g.Expect(condition).ToNot(BeNil(), "IngressDefaultCertificateSynced condition should be set")
			g.Expect(condition.Status).To(Equal(tt.expectCondition))
			g.Expect(condition.Reason).To(Equal(tt.expectReason))
		})
	}
}
