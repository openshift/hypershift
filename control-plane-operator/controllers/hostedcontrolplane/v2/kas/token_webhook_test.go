package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	fgtesting "k8s.io/component-base/featuregate/testing"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptAuthenticationTokenWebhookConfigSecret(t *testing.T) {
	const namespace = "test-namespace"

	testCases := []struct {
		name                  string
		authentication        *configv1.AuthenticationSpec
		enableExternalWebhook bool
		objects               []crclient.Object
		expectedError         string
		expectedServer        string
		expectedCA            []byte
		expectedClientCert    []byte
		expectedClientKey     []byte
	}{
		{
			name: "When External OIDC webhook authentication is enabled, it should configure the external webhook without client credentials",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			enableExternalWebhook: true,
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "external-oidc-webhook-ca", Namespace: namespace},
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("external-oidc-webhook-ca"),
					},
				},
			},
			expectedServer:     "https://external-oidc-webhook.test-namespace.svc:443/apis/oauth.openshift.io/v1/tokenreviews",
			expectedCA:         []byte("external-oidc-webhook-ca"),
			expectedClientCert: nil,
			expectedClientKey:  nil,
		},
		{
			name: "When External OIDC webhook authentication is enabled and the webhook CA secret is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			enableExternalWebhook: true,
			expectedError:         "failed to get external OIDC webhook CA secret",
		},
		{
			name: "When External OIDC webhook authentication is enabled and the webhook CA certificate is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			enableExternalWebhook: true,
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "external-oidc-webhook-ca", Namespace: namespace},
				},
			},
			expectedError: "expected tls.crt key in external OIDC webhook CA secret",
		},
		{
			name: "When integrated OAuth authentication is enabled, it should configure the OAuth API server with client credentials",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: manifests.RootCASecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						certs.CASignerCertMapKey: []byte("root-ca"),
					},
				},
				&corev1.Secret{
					ObjectMeta: manifests.OpenshiftAuthenticatorCertSecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte("authenticator-cert"),
						corev1.TLSPrivateKeyKey: []byte("authenticator-key"),
					},
				},
			},
			expectedServer:     "https://openshift-oauth-apiserver.test-namespace.svc:443/apis/oauth.openshift.io/v1/tokenreviews",
			expectedCA:         []byte("root-ca"),
			expectedClientCert: []byte("authenticator-cert"),
			expectedClientKey:  []byte("authenticator-key"),
		},
		{
			name: "When integrated OAuth authentication is enabled and the authenticator certificate secret is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			expectedError: "failed to get authenticator cert secret",
		},
		{
			name: "When integrated OAuth authentication is enabled and the root CA secret is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: manifests.OpenshiftAuthenticatorCertSecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte("authenticator-cert"),
						corev1.TLSPrivateKeyKey: []byte("authenticator-key"),
					},
				},
			},
			expectedError: "failed to get root ca cert secret",
		},
		{
			name: "When integrated OAuth authentication is enabled and the root CA certificate is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: manifests.RootCASecret(namespace).ObjectMeta,
				},
				&corev1.Secret{
					ObjectMeta: manifests.OpenshiftAuthenticatorCertSecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte("authenticator-cert"),
						corev1.TLSPrivateKeyKey: []byte("authenticator-key"),
					},
				},
			},
			expectedError: "expected ca.crt key in the root CA configMap",
		},
		{
			name: "When integrated OAuth authentication is enabled and the authenticator certificate is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: manifests.RootCASecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						certs.CASignerCertMapKey: []byte("root-ca"),
					},
				},
				&corev1.Secret{
					ObjectMeta: manifests.OpenshiftAuthenticatorCertSecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						corev1.TLSPrivateKeyKey: []byte("authenticator-key"),
					},
				},
			},
			expectedError: "expected tls.crt key in authenticator secret",
		},
		{
			name: "When integrated OAuth authentication is enabled and the authenticator private key is missing, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: manifests.RootCASecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						certs.CASignerCertMapKey: []byte("root-ca"),
					},
				},
				&corev1.Secret{
					ObjectMeta: manifests.OpenshiftAuthenticatorCertSecret(namespace).ObjectMeta,
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("authenticator-cert"),
					},
				},
			},
			expectedError: "expected tls.key key in authenticator secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, tc.enableExternalWebhook)

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for i := range tc.objects {
				clientBuilder.WithObjects(tc.objects[i])
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{Authentication: tc.authentication},
					},
				},
				Client: clientBuilder.Build(),
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Data:       map[string][]byte{},
			}

			err := adaptAuthenticationTokenWebhookConfigSecret(cpContext, secret)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			kubeconfig, err := clientcmd.Load(secret.Data[KubeconfigKey])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(kubeconfig.Clusters["local-cluster"].Server).To(Equal(tc.expectedServer))
			g.Expect(kubeconfig.Clusters["local-cluster"].CertificateAuthorityData).To(Equal(tc.expectedCA))

			context := kubeconfig.Contexts["local-context"]
			if tc.expectedClientCert == nil {
				g.Expect(context.AuthInfo).To(BeEmpty())
				g.Expect(kubeconfig.AuthInfos).To(BeEmpty())
				return
			}

			g.Expect(context.AuthInfo).To(Equal("openshift-authenticator"))
			authInfo := kubeconfig.AuthInfos[context.AuthInfo]
			g.Expect(authInfo.ClientCertificateData).To(Equal(tc.expectedClientCert))
			g.Expect(authInfo.ClientKeyData).To(Equal(tc.expectedClientKey))
		})
	}
}
