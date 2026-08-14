package capimanager

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptWebhookTLSSecret(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                   string
		existingData           map[string][]byte
		skipCertificateSigning bool
		hcpAnnotations         map[string]string
		validate               func(*testing.T, *WithT, *corev1.Secret, error)
	}{
		{
			name: "When existing certificate is present, it should preserve it",
			existingData: map[string][]byte{
				corev1.TLSCertKey:       []byte("existing-cert"),
				corev1.TLSPrivateKeyKey: []byte("existing-key"),
			},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data[corev1.TLSCertKey]).To(Equal([]byte("existing-cert")))
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).To(Equal([]byte("existing-key")))
			},
		},
		{
			name: "When existing cert is present but key is missing, it should generate new cert and key",
			existingData: map[string][]byte{
				corev1.TLSCertKey: []byte("existing-cert"),
			},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				// Should generate new cert and key, overwriting the partial data
				g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
				g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(Equal([]byte("existing-cert")))
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())
			},
		},
		{
			name: "When existing key is present but cert is missing, it should generate new cert and key",
			existingData: map[string][]byte{
				corev1.TLSPrivateKeyKey: []byte("existing-key"),
			},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				// Should generate new cert and key, overwriting the partial data
				g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(Equal([]byte("existing-key")))
			},
		},
		{
			name:                   "When skip certificate signing is enabled, it should not generate cert",
			existingData:           map[string][]byte{},
			skipCertificateSigning: true,
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data[corev1.TLSCertKey]).To(BeEmpty())
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).To(BeEmpty())
			},
		},
		{
			name:         "When no existing cert and signing is enabled, it should generate self-signed cert",
			existingData: map[string][]byte{},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())
			},
		},
		{
			name:         "When secret has no data field, it should create data map and generate cert",
			existingData: nil,
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data).ToNot(BeNil())
				g.Expect(secret.Data[corev1.TLSCertKey]).ToNot(BeEmpty())
				g.Expect(secret.Data[corev1.TLSPrivateKeyKey]).ToNot(BeEmpty())
			},
		},
		{
			name:         "When HCP has hosted cluster annotation, it should set secret annotation",
			existingData: map[string][]byte{},
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Annotations).To(HaveKey(k8sutil.HostedClusterAnnotation))
				g.Expect(secret.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("test-namespace/test-cluster"))
			},
		},
		{
			name:         "When secret has no annotations field, it should create annotations map",
			existingData: map[string][]byte{},
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
			validate: func(t *testing.T, g *WithT, secret *corev1.Secret, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Annotations).ToNot(BeNil())
				g.Expect(secret.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("test-namespace/test-cluster"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi-webhooks-tls",
					Namespace: "test-namespace",
				},
				Data: tc.existingData,
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context:                t.Context(),
				HCP:                    hcp,
				SkipCertificateSigning: tc.skipCertificateSigning,
			}

			err := adaptWebhookTLSSecret(cpContext, secret)
			tc.validate(t, g, secret, err)
		})
	}
}
