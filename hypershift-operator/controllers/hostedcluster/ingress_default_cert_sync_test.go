package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		name string
		// mutate allows a test to tweak the base HostedCluster (e.g. pre-seed a condition).
		hcluster       *hyperv1.HostedCluster
		existingSecret *corev1.Secret
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
			name: "When DefaultCertificate is cleared, a stale condition should be removed",
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
			expectSync: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctx := context.Background()

			objs := []client.Object{tt.hcluster}
			if tt.existingSecret != nil {
				objs = append(objs, tt.existingSecret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(tt.hcluster).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
				now:    metav1.Now,
			}

			controlPlaneNamespace := "clusters-test-cluster"
			err := r.reconcileIngressDefaultCertSync(ctx, tt.hcluster, upsert.New(false).CreateOrUpdate, controlPlaneNamespace)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectSync {
				dest := &corev1.Secret{}
				err := fakeClient.Get(ctx, client.ObjectKey{
					Namespace: controlPlaneNamespace,
					Name:      "service-provider-default-ingress-serving-cert",
				}, dest)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(dest.Type).To(Equal(corev1.SecretTypeTLS))
				g.Expect(dest.Data[corev1.TLSCertKey]).To(Equal(tt.existingSecret.Data[corev1.TLSCertKey]))
				g.Expect(dest.Data[corev1.TLSPrivateKeyKey]).To(Equal(tt.existingSecret.Data[corev1.TLSPrivateKeyKey]))
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
