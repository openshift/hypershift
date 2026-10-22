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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	kauthnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
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
	ExternalOIDCCELReloadTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ExternalOIDC] External OIDC", Label("external-oidc"), Serial, func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		cluster, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		caFile := internal.GetEnvVarValue("E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE")
		testUsers := internal.GetEnvVarValue("E2E_EXTERNAL_OIDC_TEST_USERS")
		if caFile == "" && testUsers == "" {
			skipIfNotOIDC(cluster)
		}
		Expect(oidcFixtureAuthentication(testCtx.Context, testCtx.MgmtClient, cluster,
			caFile, testUsers,
			featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings))).To(Succeed())
	})

	RegisterExternalOIDCTests(internal.GetTestContext)
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

// ExternalOIDCKASConfigTest verifies KAS authentication configuration matches the OIDC provider
// spec from the hosted cluster. Validates JWT authenticator issuer URL, audiences, and absence
// of OAuth webhook config. Skips on non-OIDC clusters.
func ExternalOIDCKASConfigTest(getTestCtx internal.TestContextGetter) {
	Context("KAS Authentication Configuration", Label("external-oidc"), func() {
		BeforeEach(func() {
			hc, err := getTestCtx().GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(hc)
		})

		It("should have auth-config ConfigMap with JWT authenticator matching the OIDC provider", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			expectedIssuerURL := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.URL
			Expect(expectedIssuerURL).NotTo(BeEmpty(),
				"OIDC provider issuer URL should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			// The auth-config ConfigMap is reconciled by CPO after the OIDC
			// configuration is patched onto the HostedCluster. Poll until the
			// JWT authenticator array is populated.
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "auth-config",
				}, cm)
				g.Expect(err).NotTo(HaveOccurred(),
					"auth-config ConfigMap should exist in %s", tc.ControlPlaneNamespace)

				authJSON, ok := cm.Data["auth.json"]
				g.Expect(ok).To(BeTrue(), "auth-config ConfigMap should have auth.json key")

				var authConfig map[string]interface{}
				g.Expect(json.Unmarshal([]byte(authJSON), &authConfig)).To(Succeed())

				jwtArray, ok := authConfig["jwt"].([]interface{})
				g.Expect(ok).To(BeTrue(), "auth.json should have jwt array")
				g.Expect(jwtArray).NotTo(BeEmpty(), "jwt array should not be empty")

				firstJWT, ok := jwtArray[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "first jwt entry should be a map")
				issuer, ok := firstJWT["issuer"].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "jwt entry should have issuer")
				g.Expect(issuer["url"]).To(Equal(expectedIssuerURL),
					"JWT issuer URL should match OIDC provider from hosted cluster spec")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		})

		It("should have correct audiences in JWT config", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			expectedAudiences := hc.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.Audiences
			Expect(expectedAudiences).NotTo(BeEmpty(),
				"OIDC provider audiences should not be empty on hosted cluster %s/%s", hc.Namespace, hc.Name)

			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      "auth-config",
				}, cm)).To(Succeed())

				var authConfig map[string]interface{}
				g.Expect(json.Unmarshal([]byte(cm.Data["auth.json"]), &authConfig)).To(Succeed())

				jwtArray, ok := authConfig["jwt"].([]interface{})
				g.Expect(ok).To(BeTrue(), "auth.json should have jwt array")
				g.Expect(jwtArray).NotTo(BeEmpty(), "jwt array should not be empty")

				firstJWT, ok := jwtArray[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "first jwt entry should be a map")
				issuer, ok := firstJWT["issuer"].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "jwt entry should have issuer")
				audiences, ok := issuer["audiences"].([]interface{})
				g.Expect(ok).To(BeTrue(), "JWT issuer should have audiences array")
				g.Expect(audiences).NotTo(BeEmpty(), "JWT audiences should not be empty")

				for _, expected := range expectedAudiences {
					g.Expect(audiences).To(ContainElement(string(expected)),
						"JWT audiences should contain %s from hosted cluster spec", expected)
				}
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
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
			Expect(ok).To(BeTrue(), "KAS configuration must expose apiServerArguments")
			_, hasWebhook := apiServerArgs["authentication-token-webhook-config-file"]
			Expect(hasWebhook).To(BeFalse(),
				"KAS should not have authentication-token-webhook-config-file when OIDC is configured")
		})
	})
}

// ExternalOIDCKeycloakAuthTest verifies Keycloak-based External OIDC authentication by obtaining
// fresh tokens and performing a SelfSubjectReview against the hosted cluster KAS.
// Its ephemeral Keycloak identity is shared by the ordered claim-mapping checks.
func ExternalOIDCKeycloakAuthTest(getTestCtx internal.TestContextGetter) {
	Context("Keycloak Authentication and Claims", Label("external-oidc"), Ordered, func() {
		var review *kauthnv1.SelfSubjectReview
		var userConfig *rest.Config
		var provider configv1.OIDCProvider
		var user oidcTestUser
		var cluster *hyperv1.HostedCluster
		BeforeAll(func() {
			tc := getTestCtx()
			var err error
			cluster, err = tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(cluster)
			provider = cluster.Spec.Configuration.Authentication.OIDCProviders[0]
			keycloak := keycloakForHostedCluster(tc, cluster)
			user = createOIDCTestUser(tc, keycloak, "", true)
			config, err := tc.GetHostedClusterRESTConfig(cluster)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(assertions Gomega) {
				review, userConfig, err = oidcSubjectReview(tc, keycloak, config, user)
				assertions.Expect(err).NotTo(HaveOccurred())
			}, 5*time.Minute, 15*time.Second).Should(Succeed())
		})
		It("should authenticate to KAS with a Keycloak-issued token", func() {
			Expect(review).NotTo(BeNil())
			Expect(review.Status.UserInfo.Username).NotTo(BeEmpty())
		})
		It("should map the exact verified email username with its configured prefix", func() {
			Expect(provider.ClaimMappings.Username.Prefix).NotTo(BeNil())
			Expect(review.Status.UserInfo.Username).To(Equal(provider.ClaimMappings.Username.Prefix.PrefixString + user.email))
		})
		It("should map exactly the authenticated and prefixed identity-provider groups", func() {
			Expect(review.Status.UserInfo.Groups).To(ConsistOf("system:authenticated", provider.ClaimMappings.Groups.Prefix+user.group))
		})
		It("should map UID claim with the configured expression markers", func() {
			requireOIDCFeature(getTestCtx(), cluster, "ExternalOIDCWithUIDAndExtraClaimMappings")
			Expect(provider.ClaimMappings.UID).NotTo(BeNil())
			Expect(user.subject).NotTo(BeEmpty())
			Expect(review.Status.UserInfo.UID).To(Equal("testuid-" + user.subject + "-uidtest"))
		})
		It("should map both configured extra claims", func() {
			requireOIDCFeature(getTestCtx(), cluster, "ExternalOIDCWithUIDAndExtraClaimMappings")
			Expect(review.Status.UserInfo.Extra).To(HaveKey("extratest.openshift.com/foo"))
			Expect(review.Status.UserInfo.Extra).To(HaveKey("extratest.openshift.com/bar"))
			Expect(review.Status.UserInfo.Extra["extratest.openshift.com/foo"]).To(ConsistOf(user.email))
			Expect(review.Status.UserInfo.Extra["extratest.openshift.com/bar"]).To(ConsistOf("extra-test-mark"))
		})
		It("should deny an authenticated unprivileged user access to cluster operators", func() {
			tc := getTestCtx()
			client, err := crclient.New(userConfig, crclient.Options{Scheme: tc.MgmtClient.Scheme()})
			Expect(err).NotTo(HaveOccurred())
			err = client.Get(tc.Context, crclient.ObjectKey{Name: "image-registry"}, &configv1.ClusterOperator{})
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "authenticated OIDC users must not acquire cluster-operator read privileges")
		})
	})
}

func ExternalOIDCCELReloadTest(getTestCtx internal.TestContextGetter) {
	Context("CEL claim mappings and validation reload", Label("external-oidc-lifecycle"), Ordered, func() {
		var tc *internal.TestContext
		var keycloak *oidcKeycloak
		var config *rest.Config
		var valid oidcTestUser
		BeforeAll(func() {
			tc = getTestCtx()
			skipIfNativeOriginBelow(tc, semver.MustParse("4.23.0"))
			cluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			skipIfNotOIDC(cluster)
			requireOIDCFeature(tc, cluster, "ExternalOIDCWithUpstreamParity")
			keycloak = keycloakForHostedCluster(tc, cluster)
			config, err = tc.GetHostedClusterRESTConfig(cluster)
			Expect(err).NotTo(HaveOccurred())
			valid = createOIDCTestUser(tc, keycloak, "", true)
			original := cluster.Spec.Configuration.Authentication.DeepCopy()
			update := func(testContext *internal.TestContext, authentication *configv1.AuthenticationSpec) error {
				return retry.RetryOnConflict(retry.DefaultRetry, func() error {
					current, err := testContext.GetHostedCluster()
					if err != nil {
						return err
					}
					current.Spec.Configuration.Authentication = authentication.DeepCopy()
					return testContext.MgmtClient.Update(testContext.Context, current)
				})
			}
			DeferCleanup(func() {
				cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 10*time.Minute)
				defer cancel()
				cleanupTC := *tc
				cleanupTC.Context = cleanupContext
				Expect(update(&cleanupTC, original)).To(Succeed())
				Eventually(func(assertions Gomega) {
					review, _, err := oidcSubjectReview(&cleanupTC, keycloak, config, valid)
					assertions.Expect(err).NotTo(HaveOccurred())
					assertions.Expect(original.OIDCProviders[0].ClaimMappings.Username.Prefix).NotTo(BeNil())
					assertions.Expect(review.Status.UserInfo.Username).To(Equal(original.OIDCProviders[0].ClaimMappings.Username.Prefix.PrefixString + valid.email))
				}).WithContext(cleanupContext).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(), "KAS must reload the restored authentication mappings")
			})
			updated, err := upstreamOIDCAuthentication(original)
			Expect(err).NotTo(HaveOccurred())
			Expect(update(tc, updated)).To(Succeed())
		})
		It("should reload the patched upstream CEL authentication configuration using fresh tokens", func() {
			Eventually(func(assertions Gomega) {
				review, _, err := oidcSubjectReview(tc, keycloak, config, valid)
				assertions.Expect(err).NotTo(HaveOccurred())
				assertions.Expect(review.Status.UserInfo.Username).To(Equal(valid.username))
				assertions.Expect(review.Status.UserInfo.Groups).To(ConsistOf("system:authenticated", valid.group))
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		})
		It("should authenticate a fresh verified identity with exact CEL username and groups", func() {
			user := createOIDCTestUser(tc, keycloak, "", true)
			review, _, err := oidcSubjectReview(tc, keycloak, config, user)
			Expect(err).NotTo(HaveOccurred())
			Expect(review.Status.UserInfo.Username).To(Equal(user.username))
			Expect(review.Status.UserInfo.Groups).To(ConsistOf("system:authenticated", user.group))
		})
		It("should reject an issued token whose unverified email fails the claim validation rule", func() {
			user := createOIDCTestUser(tc, keycloak, "", false)
			_, _, err := oidcSubjectReview(tc, keycloak, config, user)
			Expect(apierrors.IsUnauthorized(err)).To(BeTrue(), "claim validation must reject a validly issued but disallowed identity")
		})
		It("should reject an issued token whose forbidden username fails the user validation rule", func() {
			user := createOIDCTestUser(tc, keycloak, "cel-test-user-forbidden-", true)
			_, _, err := oidcSubjectReview(tc, keycloak, config, user)
			Expect(apierrors.IsUnauthorized(err)).To(BeTrue(), "user validation must reject a validly issued but disallowed identity")
		})
	})
}

func upstreamOIDCAuthentication(original *configv1.AuthenticationSpec) (*configv1.AuthenticationSpec, error) {
	if original == nil || len(original.OIDCProviders) == 0 {
		return nil, fmt.Errorf("OIDC authentication must contain a provider")
	}
	authentication := original.DeepCopy()
	provider := &authentication.OIDCProviders[0]
	provider.ClaimMappings.Username = configv1.UsernameClaimMapping{Expression: "claims.email.split('@')[0]"}
	provider.ClaimMappings.Groups = configv1.PrefixedClaimMapping{TokenClaimMapping: configv1.TokenClaimMapping{Expression: "claims.?groups.orValue([])"}}
	provider.ClaimValidationRules = []configv1.TokenClaimValidationRule{{Type: configv1.TokenValidationRuleTypeCEL, CEL: configv1.TokenClaimValidationCELRule{Expression: "claims.email_verified == true", Message: "email_verified claim must be true"}}}
	provider.UserValidationRules = []configv1.TokenUserValidationRule{{Expression: "!user.username.contains('forbidden')", Message: "username cannot contain the word 'forbidden'"}}
	return authentication, nil
}
