//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	kauthnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kauthnv1typedclient "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterExternalOIDCTests registers all External OIDC validation tests.
// Tests are platform-agnostic -- they skip based on authentication type, not platform.
// Panics if the hosted cluster cannot be fetched when OIDC is expected.
func RegisterExternalOIDCTests(getTestCtx internal.TestContextGetter) {
	ExternalOIDCClusterConfigTest(getTestCtx)
	ExternalOIDCOAuthNotDeployedTest(getTestCtx)
	ExternalOIDCKASConfigTest(getTestCtx)
	ExternalOIDCKeycloakAuthTest(getTestCtx)
}

var _ = Describe("External OIDC", Label("external-oidc"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		testCtx.ValidateHostedCluster()
	})

	RegisterExternalOIDCTests(func() *internal.TestContext { return testCtx })
})

// skipIfNotOIDC skips the current test if the hosted cluster does not have External OIDC configured.
func skipIfNotOIDC(hc *hyperv1.HostedCluster) {
	if hc == nil {
		Skip("no hosted cluster available")
	}
	if hc.Spec.Configuration == nil ||
		hc.Spec.Configuration.Authentication == nil ||
		hc.Spec.Configuration.Authentication.Type != configv1.AuthenticationTypeOIDC {
		Skip("External OIDC tests require authentication type OIDC")
	}
	Expect(hc.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty(),
		"hosted cluster %s/%s has authentication type OIDC but no OIDC providers configured",
		hc.Namespace, hc.Name)
}

// ExternalOIDCClusterConfigTest verifies the hosted cluster has OIDC authentication configured
// and is in Available condition. Skips on non-OIDC clusters.
func ExternalOIDCClusterConfigTest(getTestCtx internal.TestContextGetter) {
	Context("Cluster OIDC Configuration", Label("external-oidc"), func() {
		BeforeEach(func() {
			skipIfNotOIDC(getTestCtx().GetHostedCluster())
		})

		It("should have authentication type OIDC on the hosted cluster", func() {
			hc := getTestCtx().GetHostedCluster()
			Expect(hc.Spec.Configuration).NotTo(BeNil(),
				"hosted cluster %s/%s should have configuration set", hc.Namespace, hc.Name)
			Expect(hc.Spec.Configuration.Authentication).NotTo(BeNil(),
				"hosted cluster %s/%s should have authentication configured", hc.Namespace, hc.Name)
			Expect(hc.Spec.Configuration.Authentication.Type).To(Equal(configv1.AuthenticationTypeOIDC),
				"hosted cluster %s/%s authentication type should be OIDC", hc.Namespace, hc.Name)
			Expect(hc.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty(),
				"hosted cluster %s/%s should have at least one OIDC provider", hc.Namespace, hc.Name)
		})

		It("should have the hosted cluster Available", func() {
			hc := getTestCtx().GetHostedCluster()
			found := false
			for _, cond := range hc.Status.Conditions {
				if cond.Type == string(hyperv1.HostedClusterAvailable) {
					found = true
					Expect(string(cond.Status)).To(Equal("True"),
						"hosted cluster %s/%s Available condition should be True, got %s: %s",
						hc.Namespace, hc.Name, cond.Status, cond.Message)
					break
				}
			}
			Expect(found).To(BeTrue(),
				"hosted cluster %s/%s should have Available condition", hc.Namespace, hc.Name)
		})
	})
}

// ExternalOIDCOAuthNotDeployedTest verifies that OAuth-related deployments are absent from the
// control plane namespace when External OIDC is configured. Skips on non-OIDC clusters.
func ExternalOIDCOAuthNotDeployedTest(getTestCtx internal.TestContextGetter) {
	Context("OAuth Server Not Deployed", Label("external-oidc"), func() {
		BeforeEach(func() {
			skipIfNotOIDC(getTestCtx().GetHostedCluster())
		})

		It("should not have oauth-openshift deployment in the control plane namespace", func() {
			tc := getTestCtx()
			deployment := &appsv1.Deployment{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "oauth-openshift",
			}, deployment)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"oauth-openshift deployment should not exist in %s when OIDC is configured",
				tc.ControlPlaneNamespace)
		})

		It("should not have openshift-oauth-apiserver deployment in the control plane namespace", func() {
			tc := getTestCtx()
			deployment := &appsv1.Deployment{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "openshift-oauth-apiserver",
			}, deployment)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"openshift-oauth-apiserver deployment should not exist in %s when OIDC is configured",
				tc.ControlPlaneNamespace)
		})
	})
}

// ExternalOIDCKASConfigTest verifies KAS authentication configuration matches the OIDC provider
// spec from the hosted cluster. Validates JWT authenticator issuer URL, audiences, and absence
// of OAuth webhook config. Skips on non-OIDC clusters.
func ExternalOIDCKASConfigTest(getTestCtx internal.TestContextGetter) {
	Context("KAS Authentication Configuration", Label("external-oidc"), func() {
		BeforeEach(func() {
			skipIfNotOIDC(getTestCtx().GetHostedCluster())
		})

		It("should have auth-config ConfigMap with JWT authenticator matching the OIDC provider", func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()

			cm := &corev1.ConfigMap{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "auth-config",
			}, cm)
			Expect(err).NotTo(HaveOccurred(),
				"auth-config ConfigMap should exist in %s", tc.ControlPlaneNamespace)

			authJSON, ok := cm.Data["auth.json"]
			Expect(ok).To(BeTrue(), "auth-config ConfigMap should have auth.json key")

			var authConfig map[string]interface{}
			Expect(json.Unmarshal([]byte(authJSON), &authConfig)).To(Succeed())

			jwtArray, ok := authConfig["jwt"].([]interface{})
			Expect(ok).To(BeTrue(), "auth.json should have jwt array")
			Expect(jwtArray).NotTo(BeEmpty(), "jwt array should not be empty")

			// Dynamic assertion: compare against HC spec, not hardcoded values
			expectedIssuerURL := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.URL
			Expect(expectedIssuerURL).NotTo(BeEmpty(),
				"OIDC provider issuer URL should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			firstJWT, ok := jwtArray[0].(map[string]interface{})
			Expect(ok).To(BeTrue(), "first jwt entry should be a map")
			issuer, ok := firstJWT["issuer"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "jwt entry should have issuer")
			Expect(issuer["url"]).To(Equal(expectedIssuerURL),
				"JWT issuer URL should match OIDC provider from hosted cluster spec")
		})

		It("should have correct audiences in JWT config", func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()

			cm := &corev1.ConfigMap{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "auth-config",
			}, cm)).To(Succeed())

			var authConfig map[string]interface{}
			Expect(json.Unmarshal([]byte(cm.Data["auth.json"]), &authConfig)).To(Succeed())

			jwtArray, ok := authConfig["jwt"].([]interface{})
			Expect(ok).To(BeTrue(), "auth.json should have jwt array")
			Expect(jwtArray).NotTo(BeEmpty(), "jwt array should not be empty")
			firstJWT, ok := jwtArray[0].(map[string]interface{})
			Expect(ok).To(BeTrue(), "first jwt entry should be a map")
			issuer, ok := firstJWT["issuer"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "jwt entry should have issuer")
			audiences, ok := issuer["audiences"].([]interface{})
			Expect(ok).To(BeTrue(), "JWT issuer should have audiences array")
			Expect(audiences).NotTo(BeEmpty(), "JWT audiences should not be empty")

			expectedAudiences := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.Audiences
			for _, expected := range expectedAudiences {
				Expect(audiences).To(ContainElement(string(expected)),
					"JWT audiences should contain %s from hosted cluster spec", expected)
			}
		})

		It("should not have OAuth webhook authentication config", func() {
			tc := getTestCtx()
			cm := &corev1.ConfigMap{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "kas-config",
			}, cm)).To(Succeed())

			var kasConfig map[string]interface{}
			Expect(json.Unmarshal([]byte(cm.Data["config.json"]), &kasConfig)).To(Succeed())

			apiServerArgs, ok := kasConfig["apiServerArguments"].(map[string]interface{})
			if ok {
				_, hasWebhook := apiServerArgs["authentication-token-webhook-config-file"]
				Expect(hasWebhook).To(BeFalse(),
					"KAS should not have authentication-token-webhook-config-file when OIDC is configured")
			}
		})
	})
}

// ExternalOIDCKeycloakAuthTest verifies Keycloak-based External OIDC authentication by obtaining
// a token and performing a SelfSubjectReview against the hosted cluster KAS. Uses Ordered with
// BeforeAll to obtain the token once and validate claim mappings in separate It blocks.
// Skips when OIDC env vars are not configured or the cluster is not OIDC.
func ExternalOIDCKeycloakAuthTest(getTestCtx internal.TestContextGetter) {
	Context("Keycloak Authentication and Claims", Label("external-oidc"), Ordered, func() {
		var selfSubjectReview *kauthnv1.SelfSubjectReview
		var extOIDCConfig *e2eutil.ExtOIDCConfig

		BeforeAll(func() {
			tc := getTestCtx()
			hc := tc.GetHostedCluster()
			skipIfNotOIDC(hc)

			provider := hc.Spec.Configuration.Authentication.OIDCProviders[0]
			Expect(provider.Issuer.URL).NotTo(BeEmpty(),
				"OIDC provider issuer URL should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			testUsersStr := internal.GetEnvVarValue("E2E_EXTERNAL_OIDC_TEST_USERS")
			if testUsersStr == "" {
				Skip("External OIDC test users not configured")
			}

			var cliClientID, consoleClientID string
			for _, oidcClient := range provider.OIDCClients {
				switch oidcClient.ComponentName {
				case "cli":
					cliClientID = oidcClient.ClientID
				case "console":
					consoleClientID = oidcClient.ClientID
				}
			}
			Expect(cliClientID).NotTo(BeEmpty(),
				"CLI client ID not found in OIDCProviders[0].OIDCClients for %s/%s", hc.Namespace, hc.Name)
			Expect(consoleClientID).NotTo(BeEmpty(),
				"console client ID not found in OIDCProviders[0].OIDCClients for %s/%s", hc.Namespace, hc.Name)

			extOIDCConfig = &e2eutil.ExtOIDCConfig{
				ExternalOIDCProvider: e2eutil.ProviderKeycloak,
				CliClientID:          cliClientID,
				ConsoleClientID:      consoleClientID,
				IssuerURL:            provider.Issuer.URL,
				GroupPrefix:          provider.ClaimMappings.Groups.Prefix,
				TestUsers:            testUsersStr,
			}
			if provider.ClaimMappings.Username.Prefix != nil {
				extOIDCConfig.UserPrefix = provider.ClaimMappings.Username.Prefix.PrefixString
			}

			restConfig := tc.GetHostedClusterRESTConfig()
			Expect(restConfig).NotTo(BeNil(),
				"hosted cluster REST config should be available for %s/%s", hc.Namespace, hc.Name)

			// KAS may need time to load the OIDC authentication config after the HC
			// was patched in PostVersionRollout. Retry with a fresh token each attempt
			// since the Keycloak token lifetime is short (150s).
			Eventually(func(g Gomega) {
				idToken := obtainKeycloakIDToken(extOIDCConfig)
				GinkgoT().Logf("Obtained Keycloak ID token, attempting SelfSubjectReview against %s", restConfig.Host)

				authConfig := rest.AnonymousClientConfig(rest.CopyConfig(restConfig))
				authConfig.BearerToken = idToken
				authClient, err := kauthnv1typedclient.NewForConfig(authConfig)
				g.Expect(err).NotTo(HaveOccurred(), "failed to create auth client for Keycloak OIDC user")

				selfSubjectReview, err = authClient.SelfSubjectReviews().Create(
					tc.Context, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				if err != nil {
					GinkgoT().Logf("SelfSubjectReview failed: %v", err)
				}
				g.Expect(err).NotTo(HaveOccurred(), "SelfSubjectReview should succeed with Keycloak OIDC token")
			}).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		})

		It("should authenticate to KAS with a Keycloak-issued token", func() {
			Expect(selfSubjectReview).NotTo(BeNil(), "SelfSubjectReview should have been created in BeforeAll")
			Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(BeEmpty(),
				"SelfSubjectReview should return a non-empty username")
		})

		It("should map username claim correctly", func() {
			Expect(selfSubjectReview.Status.UserInfo.Username).To(ContainSubstring(extOIDCConfig.UserPrefix),
				"username should contain the configured prefix %q", extOIDCConfig.UserPrefix)
		})

		It("should map groups claim with prefix", func() {
			groups := selfSubjectReview.Status.UserInfo.Groups
			Expect(groups).NotTo(BeEmpty(), "SelfSubjectReview should return groups")
			Expect(groups).To(ContainElement(ContainSubstring(extOIDCConfig.GroupPrefix)),
				"at least one group should contain the configured prefix %q", extOIDCConfig.GroupPrefix)
		})

		It("should map UID claim correctly", func() {
			Expect(selfSubjectReview.Status.UserInfo.UID).NotTo(BeEmpty(),
				"SelfSubjectReview should return a non-empty UID")
		})
	})
}

// obtainKeycloakIDToken requests an ID token from Keycloak via the resource owner password grant.
// It picks a random test user from the configured test users string and returns the raw ID token.
func obtainKeycloakIDToken(config *e2eutil.ExtOIDCConfig) string {
	re := regexp.MustCompile(`([^:,]+):([^,]+)`)
	testUsers := re.FindAllStringSubmatch(config.TestUsers, -1)
	Expect(testUsers).NotTo(BeEmpty(), "no test users found in config")

	idx := rand.Intn(len(testUsers))
	username := testUsers[idx][1]
	password := testUsers[idx][2]
	GinkgoT().Logf("Random test user for use: %q.", username)

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	tokenURL := config.IssuerURL + "/protocol/openid-connect/token"
	formData := url.Values{
		"client_id":  {config.CliClientID},
		"grant_type": {"password"},
		"password":   {password},
		"scope":      {"openid email profile"},
		"username":   {username},
	}

	resp, err := httpClient.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(formData.Encode()))
	Expect(err).NotTo(HaveOccurred(), "failed to request token from Keycloak")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred(), "failed to read Keycloak token response")
	Expect(resp.StatusCode).To(Equal(http.StatusOK),
		fmt.Sprintf("Keycloak token request failed with status %d: %s", resp.StatusCode, string(body)))

	var tokenResp map[string]interface{}
	Expect(json.Unmarshal(body, &tokenResp)).To(Succeed(), "failed to parse Keycloak token response")

	idToken, ok := tokenResp["id_token"].(string)
	Expect(ok).To(BeTrue(), "id_token not found or not a string in Keycloak response")
	return idToken
}

