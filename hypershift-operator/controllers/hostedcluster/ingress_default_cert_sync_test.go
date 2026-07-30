package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileIngressDefaultCertSync(t *testing.T) {
	tests := []struct {
		name           string
		hcluster       *hyperv1.HostedCluster
		existingSecret *corev1.Secret
		expectError    bool
		expectSync     bool
	}{
		{
			name: "When DefaultCertificate is not set, it should be a no-op",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			expectError: false,
			expectSync:  false,
		},
		{
			name: "When OperatorConfiguration is nil, it should be a no-op",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: nil,
				},
			},
			expectError: false,
			expectSync:  false,
		},
		{
			name: "When DefaultCertificate is set with valid TLS secret, it should sync the secret",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{
								Name: "my-tls-cert",
							},
						},
					},
				},
			},
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
			expectError: false,
			expectSync:  true,
		},
		{
			name: "When source secret is missing tls.crt, it should return error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{
								Name: "bad-cert",
							},
						},
					},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-cert",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					corev1.TLSPrivateKeyKey: []byte("key-data"),
				},
			},
			expectError: true,
			expectSync:  false,
		},
		{
			name: "When source secret is missing tls.key, it should return error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{
								Name: "bad-cert",
							},
						},
					},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-cert",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey: []byte("cert-data"),
				},
			},
			expectError: true,
			expectSync:  false,
		},
		{
			name: "When source secret does not exist, it should return error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						IngressOperator: &hyperv1.IngressOperatorSpec{
							DefaultCertificate: hyperv1.IngressDefaultCertificateReference{
								Name: "nonexistent-cert",
							},
						},
					},
				},
			},
			expectError: true,
			expectSync:  false,
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
		})
	}
}
