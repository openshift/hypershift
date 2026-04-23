package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateKubeConfigWithServingCerts(t *testing.T) {
	t.Parallel()

	const (
		namespace = "test-namespace"
		serverURL = "https://api.example.com:6443"
	)

	rootCAPEM := []byte("-----BEGIN CERTIFICATE-----\nROOTCA\n-----END CERTIFICATE-----\n")
	servingCertPEM := []byte("-----BEGIN CERTIFICATE-----\nSERVINGCERT\n-----END CERTIFICATE-----\n")
	clientCertPEM := []byte("-----BEGIN CERTIFICATE-----\nCLIENTCERT\n-----END CERTIFICATE-----\n")
	clientKeyPEM := []byte("-----BEGIN RSA PRIVATE KEY-----\nCLIENTKEY\n-----END RSA PRIVATE KEY-----\n")

	testCases := []struct {
		name             string
		namedCerts       []configv1.APIServerNamedServingCert
		servingSecrets   []*corev1.Secret
		expectCACombined bool
	}{
		{
			name:             "When no named certificates are configured, it should return a kubeconfig with only the root CA",
			namedCerts:       nil,
			expectCACombined: false,
		},
		{
			name: "When named certificates are configured, it should append the serving cert to the CA bundle",
			namedCerts: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"api.custom.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: "custom-serving-cert"},
				},
			},
			servingSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "custom-serving-cert", Namespace: namespace},
					Data:       map[string][]byte{"tls.crt": servingCertPEM, "tls.key": []byte("key")},
				},
			},
			expectCACombined: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			rootCASecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "root-ca", Namespace: namespace},
				Data: map[string][]byte{
					certs.CASignerCertMapKey: rootCAPEM,
				},
			}

			clientCertSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-client-cert", Namespace: namespace},
				Data: map[string][]byte{
					corev1.TLSCertKey:       clientCertPEM,
					corev1.TLSPrivateKeyKey: clientKeyPEM,
				},
			}

			objects := []corev1.Secret{*rootCASecret, *clientCertSecret}
			for _, s := range tc.servingSecrets {
				objects = append(objects, *s)
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for i := range objects {
				clientBuilder.WithObjects(&objects[i])
			}

			var apiServerConfig *configv1.APIServerSpec
			if len(tc.namedCerts) > 0 {
				apiServerConfig = &configv1.APIServerSpec{
					ServingCerts: configv1.APIServerServingCerts{
						NamedCertificates: tc.namedCerts,
					},
				}
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: apiServerConfig,
					},
				},
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
				Client:  clientBuilder.Build(),
			}

			certSecretRef := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-client-cert", Namespace: namespace},
			}

			kubeconfigBytes, err := generateKubeConfigWithServingCerts(cpContext, certSecretRef, serverURL)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(kubeconfigBytes).ToNot(BeEmpty())

			kubeconfig, err := clientcmd.Load(kubeconfigBytes)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(kubeconfig.Clusters).To(HaveKey("cluster"))

			caData := kubeconfig.Clusters["cluster"].CertificateAuthorityData
			g.Expect(caData).To(ContainSubstring("ROOTCA"))

			if tc.expectCACombined {
				g.Expect(caData).To(ContainSubstring("SERVINGCERT"))
			} else {
				g.Expect(caData).ToNot(ContainSubstring("SERVINGCERT"))
			}

			g.Expect(kubeconfig.Clusters["cluster"].Server).To(Equal(serverURL))
			g.Expect(kubeconfig.AuthInfos).To(HaveKey("admin"))
			g.Expect(kubeconfig.AuthInfos["admin"].ClientCertificateData).To(Equal(clientCertPEM))
			g.Expect(kubeconfig.AuthInfos["admin"].ClientKeyData).To(Equal(clientKeyPEM))
		})
	}
}

func TestGenerateKubeConfigWithServingCerts_WhenRootCAIsMissing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	cpContext := controlplanecomponent.WorkloadContext{
		Context: t.Context(),
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
		},
		Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
	}

	certSecretRef := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-cert", Namespace: "test-ns"},
	}

	_, err := generateKubeConfigWithServingCerts(cpContext, certSecretRef, "https://api:6443")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to include serving certificates"))
}

func TestGenerateKubeConfigWithServingCerts_WhenClientCertIsMissing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	rootCASecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "root-ca", Namespace: "test-ns"},
		Data: map[string][]byte{
			certs.CASignerCertMapKey: []byte("-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"),
		},
	}

	cpContext := controlplanecomponent.WorkloadContext{
		Context: t.Context(),
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
		},
		Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(rootCASecret).Build(),
	}

	certSecretRef := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-cert", Namespace: "test-ns"},
	}

	_, err := generateKubeConfigWithServingCerts(cpContext, certSecretRef, "https://api:6443")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to get client cert secret"))
}
