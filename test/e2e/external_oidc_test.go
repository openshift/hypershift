//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	kauthnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kauthnv1typedclient "k8s.io/client-go/kubernetes/typed/authentication/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
)

func TestExternalOIDC(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)

	if globalOpts.ExternalOIDCProvider == "" {
		t.Skipf("skip external OIDC test if e2e.external-oidc-provider is not provided")
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 1
	clusterOpts.FeatureSet = string(configv1.Default)

	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") == "true" {
		clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
	}

	featuregates.ConfigureFeatureSet(clusterOpts.FeatureSet)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
		g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
		g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty())
		clientCfg := e2eutil.WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)
		authKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, clusterOpts.ExtOIDCConfig)
		authClient, err := kauthnv1typedclient.NewForConfig(authKubeConfig)
		g.Expect(err).NotTo(HaveOccurred())
		selfSubjectReview, err := authClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("selfSubjectReview %+v", selfSubjectReview)

		t.Run("[OCPFeatureGate:ExternalOIDC] test keycloak external OIDC", func(t *testing.T) {
			// No gates exist for ExternalOIDC as it has already been enabled by default.
			g := NewWithT(t)
			t.Logf("begin to test external OIDC %s", globalOpts.ExternalOIDCProvider)
			g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty())
			clientCfg := e2eutil.WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)
			e2eutil.ChangeClientForKeycloakExtOIDC(t, ctx, clientCfg, clusterOpts.ExtOIDCConfig)
			t.Logf("successfully get oidc user client")
		})

		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo username", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test external OIDC with external OIDC userInfo username")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Username).Should(ContainSubstring(clusterOpts.ExtOIDCConfig.UserPrefix))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo Groups", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test external OIDC userInfo Groups")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).Should(ContainElements(ContainSubstring(clusterOpts.ExtOIDCConfig.GroupPrefix)))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo UID", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test external OIDC userInfo UID")
				g.Expect(selfSubjectReview.Status.UserInfo.UID).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(e2eutil.ExternalOIDCUIDExpressionPrefix))
				g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(e2eutil.ExternalOIDCUIDExpressionSubfix))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo Extra", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test external OIDC userInfo Extra")
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(e2eutil.ExternalOIDCExtraKeyBar))
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(e2eutil.ExternalOIDCExtraKeyFoo))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC: check co status using oauth client", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test for checking co status")
				client, err := configv1client.NewForConfig(authKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())
				_, err = client.ConfigV1().ClusterOperators().Get(ctx, "image-registry", metav1.GetOptions{})
				g.Expect(err).To(HaveOccurred())
			})
		}

		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL username expression mapping", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test CEL username expression mapping")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(BeEmpty())
				// Username should be the email prefix (before @) since we configured expression: claims.email.split('@')[0]
				// doesn't contain substring
				g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(ContainSubstring("@"))
				// equals the actual email prefix
				// e2eutil.ExternalOIDCExtraKeyFoo --> claims.email expression
				emailValues := selfSubjectReview.Status.UserInfo.Extra[e2eutil.ExternalOIDCExtraKeyFoo]
				g.Expect(emailValues).NotTo(BeEmpty())
				email := emailValues[0]
				expectedUserName := strings.Split(email, "@")[0]
				g.Expect(selfSubjectReview.Status.UserInfo.Username).Should(Equal(expectedUserName))
				t.Logf("CEL username expression successfully mapped to: %s", selfSubjectReview.Status.UserInfo.Username)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL username expression mapping 1", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test CEL username expression mapping with manually created user/group")

				// Get admin credentials from environment variables
				adminUser := os.Getenv("KEYCLOAK_ADMIN_USER")
				adminPass := os.Getenv("KEYCLOAK_ADMIN_PASS")
				if adminUser == "" || adminPass == "" {
					t.Skip("KEYCLOAK_ADMIN_USER and KEYCLOAK_ADMIN_PASS environment variables must be set")
				}

				// Create admin client
				kc := e2eutil.NewKeycloakAdminClient(clusterOpts.ExtOIDCConfig.IssuerURL, adminUser, adminPass, clusterOpts.ExtOIDCConfig.IssuerCABundleFile)
				err := kc.GetAdminToken(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get admin token")

				// Create test resources tracker for automatic cleanup
				testResources := e2eutil.NewTestResources(kc)
				defer testResources.Cleanup(ctx, t)

				// Create a test group
				testGroupName := "cel-test-group-" + e2eutil.GenerateRandomPassword(8)
				groupID, err := testResources.CreateTestGroup(ctx, t, testGroupName)
				g.Expect(err).NotTo(HaveOccurred(), "failed to create test group")
				t.Logf("Created test group: %s (ID: %s)", testGroupName, groupID)

				// Create a test user with specific email
				testUsername := "cel-test-user-" + e2eutil.GenerateRandomPassword(8)
				testEmail := testUsername + "@cel-test.example.com"
				testPassword := e2eutil.GenerateRandomPassword(16)
				userID, err := testResources.CreateTestUser(ctx, t, testUsername, testEmail, testPassword)
				g.Expect(err).NotTo(HaveOccurred(), "failed to create test user")
				t.Logf("Created test user: %s (email: %s, ID: %s)", testUsername, testEmail, userID)

				// Add user to group
				err = kc.AddUserToGroup(ctx, userID, groupID)
				g.Expect(err).NotTo(HaveOccurred(), "failed to add user to group")
				t.Logf("Added user %s to group %s", testUsername, testGroupName)

				// Create a temporary auth config for this test user
				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = testUsername + ":" + testPassword

				// Authenticate as the test user
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred(), "failed to create auth client for test user")

				// Verify username expression mapping
				testSelfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get self subject review")
				t.Logf("Test user self subject review: %+v", testSelfSubjectReview.Status.UserInfo)

				// Verify username is the email prefix (before @)
				expectedUsername := strings.Split(testEmail, "@")[0]
				g.Expect(testSelfSubjectReview.Status.UserInfo.Username).Should(Equal(expectedUsername),
					"username should be email prefix from CEL expression: claims.email.split('@')[0]")
				g.Expect(testSelfSubjectReview.Status.UserInfo.Username).NotTo(ContainSubstring("@"),
					"username should not contain @ symbol")
				t.Logf("CEL username expression correctly mapped '%s' to '%s'", testEmail, testSelfSubjectReview.Status.UserInfo.Username)

				// Verify groups are mapped without prefix (due to CEL expression)
				g.Expect(testSelfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty(),
					"user should have groups from Keycloak")
				hasTestGroup := false
				for _, group := range testSelfSubjectReview.Status.UserInfo.Groups {
					if group == testGroupName {
						hasTestGroup = true
					}
					// Groups should not have prefix when using CEL expression
					g.Expect(group).NotTo(HavePrefix(clusterOpts.ExtOIDCConfig.GroupPrefix),
						"groups should not have prefix when using CEL expression")
				}
				g.Expect(hasTestGroup).To(BeTrue(), "user should be member of test group: %s", testGroupName)
				t.Logf("CEL groups expression correctly mapped groups: %v", testSelfSubjectReview.Status.UserInfo.Groups)

				// Clean up resources (will be called by defer, but we'll do it explicitly to verify deletion)
				t.Logf("Deleting test user and group")
				err = kc.DeleteUser(ctx, userID)
				g.Expect(err).NotTo(HaveOccurred(), "failed to delete test user")
				t.Logf("✓ Deleted user: %s", userID)

				err = kc.DeleteGroup(ctx, groupID)
				g.Expect(err).NotTo(HaveOccurred(), "failed to delete test group")
				t.Logf("✓ Deleted group: %s", groupID)

				// Verify deletion - trying to get user/group should fail
				t.Logf("Verifying user deletion")
				_, err = kc.GetUserByUsername(ctx, testUsername)
				g.Expect(err).To(HaveOccurred(), "user should not exist after deletion")
				g.Expect(err.Error()).To(ContainSubstring("user not found"), "error should indicate user not found")
				t.Logf("Verified user deletion: user %s not found", testUsername)

				t.Logf("Verifying group deletion")
				_, err = kc.GetGroupByName(ctx, testGroupName)
				g.Expect(err).To(HaveOccurred(), "group should not exist after deletion")
				g.Expect(err.Error()).To(ContainSubstring("group not found"), "error should indicate group not found")
				t.Logf("Verified group deletion: group %s not found", testGroupName)

				t.Logf("Test completed successfully: CEL username expression mapping verified with manual user/group creation and cleanup")
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL groups expression mapping", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test CEL groups expression mapping")
				// Groups expression uses: claims.?groups.orValue([])
				// If the token has groups, they should be present without prefix (no prefix in CEL expression)
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression).NotTo(BeEmpty())
				t.Logf("CEL groups expression configured: %s", hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression)
				// Verify the groups are actually mapped correctly - should exist and have no prefix
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty())
				// Groups should NOT contain the prefix since we're using CEL expression without prefix
				for _, group := range selfSubjectReview.Status.UserInfo.Groups {
					g.Expect(group).NotTo(HavePrefix(clusterOpts.ExtOIDCConfig.GroupPrefix))
				}
				t.Logf("CEL groups expression successfully mapped groups without prefix: %v", selfSubjectReview.Status.UserInfo.Groups)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test claim validation rules", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test claim validation rules")
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules).NotTo(BeEmpty())
				claimRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules
				g.Expect(claimRules).Should(HaveLen(2))

				// Verify configuration - rules should be CEL type with expected expressions
				g.Expect(claimRules[0].Type).Should(Equal(configv1.TokenValidationRuleTypeCEL))
				g.Expect(claimRules[0].CEL.Expression).Should(Equal(e2eutil.ClaimValidationExprEmailExists))

				g.Expect(claimRules[1].Type).Should(Equal(configv1.TokenValidationRuleTypeCEL))
				g.Expect(claimRules[1].CEL.Expression).Should(Equal(e2eutil.ClaimValidationExprEmailVerified))

				// Verify behavior - authentication succeeded, proving claim validation rules passed
				// Rule 1 validates email exists and is non-empty
				emailValues := selfSubjectReview.Status.UserInfo.Extra[e2eutil.ExternalOIDCExtraKeyFoo]
				g.Expect(emailValues).NotTo(BeEmpty(), "email claim should exist (validated by rule 1)")
				g.Expect(emailValues[0]).NotTo(BeEmpty(), "email claim should be non-empty (validated by rule 1)")
				// Rule 2 validates email_verified == true (implicit - authentication succeeded)
				t.Logf("Claim validation rules successfully validated token with email: %s", emailValues[0])
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test user validation rules", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test user validation rules")
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules).NotTo(BeEmpty())
				userRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules
				g.Expect(userRules).Should(HaveLen(1))

				// Verify configuration - rule should use expected CEL expression
				g.Expect(userRules[0].Expression).Should(Equal(e2eutil.UserValidationExprNoSystemPrefix))

				// Verify behavior - authentication succeeded, proving user validation rule passed
				// Rule validates username doesn't start with 'system:'
				g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(HavePrefix("system:"), "username should not have system: prefix (validated by user rule)")
				t.Logf("User validation rules successfully validated user: %s", selfSubjectReview.Status.UserInfo.Username)
			})
		}
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}
