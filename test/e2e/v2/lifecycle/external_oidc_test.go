//go:build e2ev2

package lifecycle

import (
	"testing"

	. "github.com/onsi/gomega"

	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKeycloakAuthentication(t *testing.T) {
	for _, additionalClaims := range []bool{false, true} {
		name := "When additional claim mappings are disabled, it should configure native OIDC without UID or extras"
		if additionalClaims {
			name = "When additional claim mappings are enabled, it should preserve UID and extra expressions"
		}
		t.Run(name, func(t *testing.T) {
			config := &v2util.KeycloakConfig{IssuerURL: "https://keycloak.apps.hypershift.devcluster.openshift.com/realms/master", CLIClientID: "hypershift-cli", ConsoleClientID: "hypershift-console"}
			authentication := keycloakAuthentication(config, additionalClaims)
			assertions := NewWithT(t)
			assertions.Expect(authentication.Type).To(Equal(configv1.AuthenticationTypeOIDC))
			assertions.Expect(authentication.OIDCProviders).To(HaveLen(1))
			provider := authentication.OIDCProviders[0]
			assertions.Expect(provider.Name).To(Equal("keycloak oidc server"))
			assertions.Expect(provider.Issuer.URL).To(Equal(config.IssuerURL))
			assertions.Expect(provider.Issuer.CertificateAuthority.Name).To(Equal("oidc-ca"))
			assertions.Expect(provider.Issuer.Audiences).To(Equal([]configv1.TokenAudience{"hypershift-cli", "hypershift-console"}))
			assertions.Expect(provider.OIDCClients).To(HaveLen(2))
			assertions.Expect(provider.OIDCClients[0].ClientID).To(Equal(config.CLIClientID))
			assertions.Expect(provider.OIDCClients[0].ComponentName).To(Equal("cli"))
			assertions.Expect(provider.OIDCClients[1].ClientID).To(Equal(config.ConsoleClientID))
			assertions.Expect(provider.OIDCClients[1].ClientSecret.Name).To(Equal("console-secret"))
			assertions.Expect(provider.OIDCClients[1].ExtraScopes).To(Equal([]string{"email"}))
			assertions.Expect(provider.ClaimMappings.Groups.Claim).To(Equal("groups"))
			assertions.Expect(provider.ClaimMappings.Groups.Prefix).To(Equal("oidc-groups-test:"))
			assertions.Expect(provider.ClaimMappings.Username.Claim).To(Equal("email"))
			assertions.Expect(provider.ClaimMappings.Username.PrefixPolicy).To(Equal(configv1.Prefix))
			assertions.Expect(provider.ClaimMappings.Username.Prefix.PrefixString).To(Equal("oidc-user-test:"))
			if additionalClaims {
				assertions.Expect(provider.ClaimMappings.UID).To(Equal(&configv1.TokenClaimOrExpressionMapping{Expression: `"testuid-" + claims.sub + "-uidtest"`}))
				assertions.Expect(provider.ClaimMappings.Extra).To(Equal([]configv1.ExtraMapping{{Key: "extratest.openshift.com/bar", ValueExpression: `"extra-test-mark"`}, {Key: "extratest.openshift.com/foo", ValueExpression: "claims.email"}}))
			} else {
				assertions.Expect(provider.ClaimMappings.UID).To(BeNil())
				assertions.Expect(provider.ClaimMappings.Extra).To(BeEmpty())
			}
		})
	}
}

func TestExternalOIDCTransitionComplete(t *testing.T) {
	t.Parallel()

	const (
		controlPlaneNamespace = "clusters-example"
		issuerURL             = "https://keycloak.apps.example.com/realms/master"
	)

	readyAuthConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
		Data: map[string]string{
			"auth.json": `{"jwt":[{"issuer":{"url":"https://keycloak.apps.example.com/realms/master"}}]}`,
		},
	}
	readyKASConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
		Data:       map[string]string{"config.json": `{"apiServerArguments":{}}`},
	}

	tests := []struct {
		name      string
		objects   []crclient.Object
		wantReady bool
		wantError bool
	}{
		{
			name:      "When OAuth deployments are absent and KAS uses the expected issuer, it should report the transition complete",
			objects:   []crclient.Object{readyAuthConfig.DeepCopy(), readyKASConfig.DeepCopy()},
			wantReady: true,
		},
		{
			name: "When an OAuth deployment still exists, it should report the transition incomplete",
			objects: []crclient.Object{
				readyAuthConfig.DeepCopy(),
				readyKASConfig.DeepCopy(),
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "oauth-openshift"}},
			},
		},
		{
			name: "When KAS still uses the previous authentication configuration, it should report the transition incomplete",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
					Data:       map[string]string{"auth.json": `{"jwt":[]}`},
				},
				readyKASConfig.DeepCopy(),
			},
		},
		{
			name: "When KAS still uses OAuth webhook authentication, it should report the transition incomplete",
			objects: []crclient.Object{
				readyAuthConfig.DeepCopy(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
					Data: map[string]string{
						"config.json": `{"apiServerArguments":{"authentication-token-webhook-config-file":["/etc/kubernetes/auth-token-webhook-config.yaml"]}}`,
					},
				},
			},
		},
		{
			name: "When the authentication configuration is malformed, it should return an error",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
					Data:       map[string]string{"auth.json": "{"},
				},
			},
			wantError: true,
		},
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding core API to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding apps API to scheme: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			gotReady, err := externalOIDCTransitionComplete(t.Context(), cl, controlPlaneNamespace, issuerURL)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("externalOIDCTransitionComplete returned an unexpected error: %v", err)
			}
			if gotReady != tt.wantReady {
				t.Errorf("externalOIDCTransitionComplete returned %t, want %t", gotReady, tt.wantReady)
			}
		})
	}
}
