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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/podspec"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	kauthnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kauthnv1typedclient "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

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

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ExternalOIDC] External OIDC", Label("external-oidc"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
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
			hc, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(hc)
		})

		It("should have authentication type OIDC on the hosted cluster", func() {
			hc, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
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
			hc, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
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
			hc, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(hc)
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

// ExternalOIDCKASConfigTest verifies direct KAS or webhook authentication configuration
// matches the hosted cluster's OIDC provider, including issuer, audiences, and KAS wiring.
// Skips on non-OIDC clusters.
func ExternalOIDCKASConfigTest(getTestCtx internal.TestContextGetter) {
	Context("KAS Authentication Configuration", Label("external-oidc"), func() {
		var (
			usesWebhook    bool
			apiServerArgs  map[string][]string
			authConfigName string
			authConfigKey  string
			authConfig     struct {
				JWT []struct {
					Issuer struct {
						URL       string   `json:"url"`
						Audiences []string `json:"audiences"`
					} `json:"issuer"`
				} `json:"jwt"`
			}
		)

		BeforeEach(func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(hc)

			cm := &corev1.ConfigMap{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "kas-config",
			}, cm)).To(Succeed())
			var kasConfig struct {
				APIServerArguments map[string][]string `json:"apiServerArguments"`
			}
			Expect(json.Unmarshal([]byte(cm.Data["config.json"]), &kasConfig)).To(Succeed())
			apiServerArgs = kasConfig.APIServerArguments

			// Use the control plane's configuration, not this test binary's feature
			// gates, so the same tests cover both OIDC architectures.
			_, usesWebhook = apiServerArgs["authentication-token-webhook-config-file"]
			_, usesDirectOIDC := apiServerArgs["authentication-config"]
			Expect(usesWebhook).NotTo(Equal(usesDirectOIDC),
				"KAS in %s should configure exactly one of direct OIDC or token webhook authentication",
				tc.ControlPlaneNamespace)
			authConfigName, authConfigKey = "auth-config", "auth.json"
			if usesWebhook {
				authConfigName, authConfigKey = "external-oidc-webhook-auth-config", "auth-config.json"
			}
			cm = &corev1.ConfigMap{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      authConfigName,
			}, cm)).To(Succeed(), "authentication ConfigMap %s/%s should exist", tc.ControlPlaneNamespace, authConfigName)
			Expect(cm.Data).To(HaveKey(authConfigKey), "ConfigMap %s should contain %s", authConfigName, authConfigKey)
			authConfig.JWT = nil
			Expect(json.Unmarshal([]byte(cm.Data[authConfigKey]), &authConfig)).To(Succeed())
			Expect(authConfig.JWT).NotTo(BeEmpty(), "ConfigMap %s should contain JWT authenticators", authConfigName)
		})

		It("should have a JWT authenticator matching the OIDC provider", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			expectedIssuerURL := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.URL
			Expect(expectedIssuerURL).NotTo(BeEmpty(),
				"OIDC provider issuer URL should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			Expect(authConfig.JWT[0].Issuer.URL).To(Equal(expectedIssuerURL),
				"JWT issuer URL in %s should match the hosted cluster's OIDC provider", authConfigName)
		})

		It("should have correct audiences in JWT config", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			expectedAudiences := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.Audiences
			Expect(expectedAudiences).NotTo(BeEmpty(),
				"OIDC provider audiences should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			Expect(authConfig.JWT[0].Issuer.Audiences).NotTo(BeEmpty(), "JWT audiences in %s should not be empty", authConfigName)
			for _, expected := range expectedAudiences {
				Expect(authConfig.JWT[0].Issuer.Audiences).To(ContainElement(string(expected)),
					"JWT audiences in %s should contain %s from the hosted cluster spec", authConfigName, expected)
			}
		})

		It("should configure KAS to use the OIDC authenticator", func() {
			tc := getTestCtx()
			if !usesWebhook {
				Expect(apiServerArgs["authentication-config"]).To(Equal([]string{"/etc/kubernetes/auth/auth.json"}))
				Expect(apiServerArgs).NotTo(HaveKey("authentication-token-webhook-config-file"))
				return
			}

			Expect(apiServerArgs).NotTo(HaveKey("authentication-config"))
			Expect(apiServerArgs["authentication-token-webhook-config-file"]).To(Equal([]string{"/etc/kubernetes/auth-token-webhook/kubeconfig"}))
			Expect(apiServerArgs["authentication-token-webhook-version"]).To(Equal([]string{"v1"}))
			secret := &corev1.Secret{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "kas-authentication-token-webhook-config",
			}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("kubeconfig"))
			kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
			Expect(err).NotTo(HaveOccurred())
			currentContext := kubeconfig.Contexts[kubeconfig.CurrentContext]
			Expect(currentContext).NotTo(BeNil(), "token webhook kubeconfig should have a current context")
			cluster := kubeconfig.Clusters[currentContext.Cluster]
			Expect(cluster).NotTo(BeNil(), "token webhook kubeconfig should reference an existing cluster")
			Expect(cluster.Server).To(Equal(fmt.Sprintf(
				"https://external-oidc-webhook.%s.svc:443/apis/oauth.openshift.io/v1/tokenreviews", tc.ControlPlaneNamespace)))
			Expect(cluster.CertificateAuthorityData).NotTo(BeEmpty(), "token webhook kubeconfig should trust the webhook CA")
			Expect(currentContext.AuthInfo).To(BeEmpty(), "external OIDC webhook should not require client authentication")
			Expect(kubeconfig.AuthInfos).To(BeEmpty(), "external OIDC webhook kubeconfig should not contain client credentials")
		})

		It("should have the expected external OIDC webhook deployment state", func() {
			tc := getTestCtx()
			deployment := &appsv1.Deployment{}
			err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      "external-oidc-webhook",
			}, deployment)
			if !usesWebhook {
				Expect(apierrors.IsNotFound(err)).To(BeTrue(),
					"external-oidc-webhook should not be deployed in %s when KAS authenticates OIDC tokens directly", tc.ControlPlaneNamespace)
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(deployment.Spec.Replicas).NotTo(BeNil(), "external-oidc-webhook deployment should specify replicas")
			Expect(*deployment.Spec.Replicas).To(BeNumerically(">", 0), "external-oidc-webhook deployment should not be scaled to zero")
			Expect(podspec.IsDeploymentReady(tc.Context, deployment)).To(BeTrue(),
				"external-oidc-webhook deployment in %s should have all desired replicas ready and updated", tc.ControlPlaneNamespace)
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
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
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

			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred())

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
