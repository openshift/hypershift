package extoidc

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	authenticationv1alpha1 "github.com/openshift/api/authentication/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptAuthConfig(t *testing.T) {
	t.Run("When cluster configuration is absent, it should leave the ConfigMap unchanged", func(t *testing.T) {
		g := NewWithT(t)
		cpContext := newAuthWorkloadContext(t, nil)
		cpContext.HCP.Spec.Configuration = nil
		authConfigMap := &corev1.ConfigMap{Data: map[string]string{"existing": "value"}}

		g.Expect(adaptAuthConfig(cpContext, authConfigMap)).To(Succeed())
		g.Expect(authConfigMap.Data).To(Equal(map[string]string{"existing": "value"}))
	})

	t.Run("When authentication configuration is absent, it should leave the ConfigMap unchanged", func(t *testing.T) {
		g := NewWithT(t)
		authConfigMap := &corev1.ConfigMap{Data: map[string]string{"existing": "value"}}

		g.Expect(adaptAuthConfig(newAuthWorkloadContext(t, nil), authConfigMap)).To(Succeed())
		g.Expect(authConfigMap.Data).To(Equal(map[string]string{"existing": "value"}))
	})

	t.Run("When no OIDC providers are configured, it should write an empty authentication configuration", func(t *testing.T) {
		g := NewWithT(t)
		authConfigMap := &corev1.ConfigMap{}

		err := adaptAuthConfig(newAuthWorkloadContext(t, &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
		}), authConfigMap)
		g.Expect(err).ToNot(HaveOccurred())

		generated := &authenticationv1alpha1.AuthenticationConfiguration{}
		g.Expect(json.Unmarshal([]byte(authConfigMap.Data[authConfigDataKey]), generated)).To(Succeed())
		g.Expect(generated.JWT).To(BeEmpty())
	})

	t.Run("When an OIDC provider references a certificate authority, it should write the certificate authority", func(t *testing.T) {
		g := NewWithT(t)
		issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer issuer.Close()

		certificateAuthority := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Certificate().Raw})
		caConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "issuer-ca", Namespace: "test-ns"},
			Data:       map[string]string{certs.UserCABundleMapKey: string(certificateAuthority)},
		}
		authConfigMap := &corev1.ConfigMap{}

		err := adaptAuthConfig(newAuthWorkloadContext(t, &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
			OIDCProviders: []configv1.OIDCProvider{{
				Name: "issuer",
				Issuer: configv1.TokenIssuer{
					URL:                  issuer.URL,
					CertificateAuthority: configv1.ConfigMapNameReference{Name: caConfigMap.Name},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "sub",
						PrefixPolicy: configv1.NoPrefix,
					},
				},
			}},
		}, caConfigMap), authConfigMap)
		g.Expect(err).ToNot(HaveOccurred())

		generated := &authenticationv1alpha1.AuthenticationConfiguration{}
		g.Expect(json.Unmarshal([]byte(authConfigMap.Data[authConfigDataKey]), generated)).To(Succeed())
		g.Expect(generated.JWT).To(HaveLen(1))
		g.Expect(generated.JWT[0].Issuer.URL).To(Equal(issuer.URL))
		g.Expect(generated.JWT[0].Issuer.CertificateAuthority).To(Equal(string(certificateAuthority)))
	})

	t.Run("When a referenced CA ConfigMap does not contain the bundle, it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		caConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "issuer-ca", Namespace: "test-ns"},
		}

		err := adaptAuthConfig(newAuthWorkloadContext(t, &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
			OIDCProviders: []configv1.OIDCProvider{{
				Name: "issuer",
				Issuer: configv1.TokenIssuer{
					CertificateAuthority: configv1.ConfigMapNameReference{Name: caConfigMap.Name},
				},
			}},
		}, caConfigMap), &corev1.ConfigMap{})
		g.Expect(err).To(MatchError(ContainSubstring(`CA configmap "issuer-ca" key "ca-bundle.crt" is missing`)))
	})

	t.Run("When an OIDC provider uses the service account issuer, it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		const issuerURL = "https://issuer.example.com"
		cpContext := newAuthWorkloadContext(t, &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
			OIDCProviders: []configv1.OIDCProvider{{
				Name: "issuer",
				Issuer: configv1.TokenIssuer{
					URL: issuerURL,
				},
			}},
		})
		cpContext.HCP.Spec.IssuerURL = issuerURL

		err := adaptAuthConfig(cpContext, &corev1.ConfigMap{})
		g.Expect(err).To(MatchError(ContainSubstring("issuer url cannot overlap with the ServiceAccount issuer url")))
	})
}

func newAuthWorkloadContext(t *testing.T, authentication *configv1.AuthenticationSpec, objects ...client.Object) component.WorkloadContext {
	t.Helper()

	return component.WorkloadContext{
		Context: context.Background(),
		Client:  fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{
				Configuration: &hyperv1.ClusterConfiguration{Authentication: authentication},
			},
		},
	}
}
