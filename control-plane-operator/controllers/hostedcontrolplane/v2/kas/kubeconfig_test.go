package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
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

func TestGenerateAuthenticationTokenWebhookKubeconfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		crtBytes         []byte
		keyBytes         []byte
		expectedAuthInfo string
	}{
		{
			name:             "When client credentials are supplied, it should include them in the kubeconfig",
			crtBytes:         []byte("client-cert"),
			keyBytes:         []byte("client-key"),
			expectedAuthInfo: "openshift-authenticator",
		},
		{
			name: "When client credentials are not supplied, it should omit client authentication",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			kubeconfigBytes, err := generateAuthenticationTokenWebhookKubeconfig(
				"https://webhook.example.com:443/apis/oauth.openshift.io/v1/tokenreviews",
				tc.crtBytes,
				tc.keyBytes,
				[]byte("webhook-ca"),
			)
			g.Expect(err).ToNot(HaveOccurred())

			kubeconfig, err := clientcmd.Load(kubeconfigBytes)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(kubeconfig.Clusters["local-cluster"].Server).To(Equal("https://webhook.example.com:443/apis/oauth.openshift.io/v1/tokenreviews"))
			g.Expect(kubeconfig.Clusters["local-cluster"].CertificateAuthorityData).To(Equal([]byte("webhook-ca")))
			g.Expect(kubeconfig.Contexts["local-context"].AuthInfo).To(Equal(tc.expectedAuthInfo))

			if tc.expectedAuthInfo == "" {
				g.Expect(kubeconfig.AuthInfos).To(BeEmpty())
				return
			}

			authInfo := kubeconfig.AuthInfos[tc.expectedAuthInfo]
			g.Expect(authInfo.ClientCertificateData).To(Equal(tc.crtBytes))
			g.Expect(authInfo.ClientKeyData).To(Equal(tc.keyBytes))
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

func TestAdaptBootstrapKubeconfigSecret(t *testing.T) {
	const namespace = "test-ns"

	rootCAPEM := []byte("-----BEGIN CERTIFICATE-----\nROOTCA\n-----END CERTIFICATE-----\n")
	clientCertPEM := []byte("-----BEGIN CERTIFICATE-----\nCLIENT\n-----END CERTIFICATE-----\n")
	clientKeyPEM := []byte("-----BEGIN RSA PRIVATE KEY-----\nKEY\n-----END RSA PRIVATE KEY-----\n")

	baseObjects := func() []corev1.Secret {
		return []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "root-ca", Namespace: namespace},
				Data:       map[string][]byte{certs.CASignerCertMapKey: rootCAPEM},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "kas-bootstrap-client", Namespace: namespace},
				Data:       map[string][]byte{corev1.TLSCertKey: clientCertPEM, corev1.TLSPrivateKeyKey: clientKeyPEM},
			},
		}
	}

	int32Ptr := func(v int32) *int32 { return &v }
	strPtr := func(v string) *string { return &v }

	testCases := []struct {
		name             string
		featureSet       string
		platformType     hyperv1.PlatformType
		endpointAccess   hyperv1.AWSEndpointAccessType
		apiServerPort    *int32
		advertiseAddress *string
		serviceNetwork   string
		expectedServer   string
	}{
		{
			name:           "When gate is OFF on Agent platform, it should use external URL",
			featureSet:     string(configv1.Default),
			platformType:   hyperv1.AgentPlatform,
			expectedServer: "https://api.test.example.com:6443",
		},
		{
			name:           "When gate is ON on Agent platform, it should use advertise address",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.AgentPlatform,
			expectedServer: "https://" + config.DefaultAdvertiseIPv4Address + ":6443",
		},
		{
			name:           "When gate is ON on None platform, it should use advertise address",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.NonePlatform,
			expectedServer: "https://" + config.DefaultAdvertiseIPv4Address + ":6443",
		},
		{
			name:           "When gate is ON on KubeVirt platform, it should use advertise address",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.KubevirtPlatform,
			expectedServer: "https://" + config.DefaultAdvertiseIPv4Address + ":6443",
		},
		{
			name:           "When gate is ON on OpenStack platform, it should use advertise address",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.OpenStackPlatform,
			expectedServer: "https://" + config.DefaultAdvertiseIPv4Address + ":6443",
		},
		{
			name:           "When gate is ON on AWS platform, it should use external URL",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.AWSPlatform,
			endpointAccess: hyperv1.Public,
			expectedServer: "https://api.test.example.com:6443",
		},
		{
			name:           "When gate is ON on Azure platform, it should use external URL",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.AzurePlatform,
			expectedServer: "https://api.test.example.com:6443",
		},
		{
			name:           "When gate is ON on GCP platform, it should use external URL",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.GCPPlatform,
			expectedServer: "https://api.test.example.com:6443",
		},
		{
			name:           "When gate is ON with apiServer.port 443 on Agent platform, it should use port 443",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.AgentPlatform,
			apiServerPort:  int32Ptr(443),
			expectedServer: "https://" + config.DefaultAdvertiseIPv4Address + ":443",
		},
		{
			name:           "When gate is ON with IPv6 service network, it should use IPv6 advertise address",
			featureSet:     string(configv1.TechPreviewNoUpgrade),
			platformType:   hyperv1.AgentPlatform,
			serviceNetwork: "fd01::/112",
			expectedServer: "https://[" + config.DefaultAdvertiseIPv6Address + "]:6443",
		},
		{
			name:             "When gate is ON with custom advertise address, it should use custom address",
			featureSet:       string(configv1.TechPreviewNoUpgrade),
			platformType:     hyperv1.AgentPlatform,
			advertiseAddress: strPtr("10.0.0.1"),
			expectedServer:   "https://10.0.0.1:6443",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			featuregates.ConfigureFeatureSet(tc.featureSet)
			t.Cleanup(func() { featuregates.ConfigureFeatureSet(string(configv1.Default)) })

			serviceNet := "10.96.0.0/12"
			if tc.serviceNetwork != "" {
				serviceNet = tc.serviceNetwork
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: namespace},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR(serviceNet)}},
					},
				},
			}
			if tc.apiServerPort != nil || tc.advertiseAddress != nil {
				hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{
					Port:             tc.apiServerPort,
					AdvertiseAddress: tc.advertiseAddress,
				}
			}
			if tc.platformType == hyperv1.AWSPlatform {
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					EndpointAccess: tc.endpointAccess,
				}
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for _, obj := range baseObjects() {
				o := obj
				clientBuilder.WithObjects(&o)
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
				Client:  clientBuilder.Build(),
				InfraStatus: infra.InfrastructureStatus{
					APIHost: "api.test.example.com",
					APIPort: 6443,
				},
			}

			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-kubeconfig", Namespace: namespace}}
			err := adaptBootstrapKubeconfigSecret(cpContext, secret)
			g.Expect(err).ToNot(HaveOccurred())

			kubeconfig, err := clientcmd.Load(secret.Data[KubeconfigKey])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(kubeconfig.Clusters["cluster"].Server).To(Equal(tc.expectedServer))
		})
	}
}
