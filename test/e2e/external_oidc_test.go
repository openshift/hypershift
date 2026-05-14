//go:build e2e

package e2e

import (
	"context"
	"os"
	"slices"
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

		// Setup Keycloak admin client
		kc, err := e2eutil.SetupKeycloakAdminClientFromCluster(ctx, t, mgtClient, clusterOpts.ExtOIDCConfig)
		if err != nil {
			t.Skipf("Could not setup Keycloak admin client: %v", err)
		}

		t.Run("[OCPFeatureGate:ExternalOIDC] test keycloak external OIDC", func(t *testing.T) {
			// No gates exist for ExternalOIDC as it has already been enabled by default.
			t.Logf("begin to test external OIDC %s", globalOpts.ExternalOIDCProvider)
			e2eutil.ChangeClientForKeycloakExtOIDC(t, ctx, clientCfg, clusterOpts.ExtOIDCConfig)
			t.Logf("successfully get oidc user client")
		})

		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {

			// Since this username/group behavior differs between ExternalODICWithUIDandExtraClaimMappings and
			// ExternalOIDCWithUpstreamParity feature gates, we should put this test behind a feature
			// gate check.
			if !featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
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
			}

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

				// Setup: Create test resources with automatic cleanup
				testResources := e2eutil.NewTestResources(kc)
				defer testResources.Cleanup(ctx, t)

				// Setup: Create authenticated test user with group
				testUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "cel-test-user", "cel-test-group", clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify: Username is email prefix (before @)
				expectedUsername := strings.Split(testUser.Email, "@")[0]
				g.Expect(testUser.SelfSubjectReview.Status.UserInfo.Username).Should(Equal(expectedUsername),
					"username should be email prefix from CEL expression: claims.email.split('@')[0]")
				g.Expect(testUser.SelfSubjectReview.Status.UserInfo.Username).NotTo(ContainSubstring("@"),
					"username should not contain @ symbol")
				t.Logf("CEL username expression correctly mapped '%s' to '%s'", testUser.Email, testUser.SelfSubjectReview.Status.UserInfo.Username)

				// Edge case test: preferred_username vs email-derived username mismatch
				// Tests that CEL expression uses claims.email, not claims.preferred_username
				t.Logf("Edge case test: Creating user with preferred_username different from email local part")
				preferredUsername := "cel-preferred-" + e2eutil.GenerateRandomPassword(8)
				actualEmail := "cel-email-" + e2eutil.GenerateRandomPassword(8) + "@test.example.com"
				preferredPassword := e2eutil.GenerateRandomPassword(16)

				// In Keycloak, the 'username' field becomes the 'preferred_username' claim
				// So we create a user where username != email local part
				_, err = testResources.CreateTestUser(ctx, t, preferredUsername, actualEmail, preferredPassword)
				g.Expect(err).NotTo(HaveOccurred())
				t.Logf("Created user: preferred_username='%s', email='%s'", preferredUsername, actualEmail)

				// Authenticate as this user
				preferredAuthConfig := *clusterOpts.ExtOIDCConfig
				preferredAuthConfig.TestUsers = preferredUsername + ":" + preferredPassword
				preferredKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &preferredAuthConfig)
				preferredAuthClient, err := kauthnv1typedclient.NewForConfig(preferredKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				preferredReview, err := preferredAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred())

				// Verify: K8s username should come from email claim, NOT preferred_username claim
				expectedUsername = strings.Split(actualEmail, "@")[0]
				g.Expect(preferredReview.Status.UserInfo.Username).Should(Equal(expectedUsername),
					"username should be derived from email claim via CEL expression, not from preferred_username claim")
				g.Expect(preferredReview.Status.UserInfo.Username).NotTo(Equal(preferredUsername),
					"username should NOT equal preferred_username when they differ")
				t.Logf("✓ CEL expression correctly used email claim over preferred_username: K8s username='%s' (from email='%s'), preferred_username='%s'",
					preferredReview.Status.UserInfo.Username, actualEmail, preferredUsername)

				// Verify: Groups are mapped without prefix
				g.Expect(testUser.SelfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty(),
					"user should have groups from Keycloak")
				hasTestGroup := false
				for _, group := range testUser.SelfSubjectReview.Status.UserInfo.Groups {
					if group == testUser.GroupName {
						hasTestGroup = true
					}
					g.Expect(group).NotTo(HavePrefix(clusterOpts.ExtOIDCConfig.GroupPrefix),
						"groups should not have prefix when using CEL expression")
				}
				g.Expect(hasTestGroup).To(BeTrue(), "user should be member of test group: %s", testUser.GroupName)
				t.Logf("CEL groups expression correctly mapped groups: %v", testUser.SelfSubjectReview.Status.UserInfo.Groups)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL groups expression mapping", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test CEL groups expression mapping")

				// Setup: Create test resources with automatic cleanup
				testResources := e2eutil.NewTestResources(kc)
				defer testResources.Cleanup(ctx, t)

				// Verify: Groups expression is configured
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression).NotTo(BeEmpty())
				t.Logf("CEL groups expression configured: %s", hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression)

				// Setup: Create authenticated test user with group
				testUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "cel-groups-test-user", "cel-groups-test-group", clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify: User has groups from Keycloak
				g.Expect(testUser.SelfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty(),
					"user should have groups from Keycloak")

				// Verify: Groups are mapped without prefix (CEL expression removes prefix)
				for _, group := range testUser.SelfSubjectReview.Status.UserInfo.Groups {
					g.Expect(group).NotTo(HavePrefix(clusterOpts.ExtOIDCConfig.GroupPrefix),
						"groups should not have prefix when using CEL expression")
				}

				// Verify: User is member of the test group
				hasTestGroup := slices.Contains(testUser.SelfSubjectReview.Status.UserInfo.Groups, testUser.GroupName)
				g.Expect(hasTestGroup).To(BeTrue(), "user should be member of test group: %s", testUser.GroupName)
				t.Logf("CEL groups expression successfully mapped groups without prefix: %v", testUser.SelfSubjectReview.Status.UserInfo.Groups)

				// Negative test: User without groups should still authenticate
				// The CEL expression claims.?groups.orValue([]) handles missing groups claim gracefully
				t.Logf("Negative test: Creating user without group membership")
				noGroupUsername := "cel-no-groups-" + e2eutil.GenerateRandomPassword(8)
				noGroupEmail := noGroupUsername + "@test.example.com"
				noGroupPassword := e2eutil.GenerateRandomPassword(16)
				_, err = testResources.CreateTestUser(ctx, t, noGroupUsername, noGroupEmail, noGroupPassword)
				g.Expect(err).NotTo(HaveOccurred())

				// Authenticate user without groups - should SUCCEED with empty groups
				noGroupAuthConfig := *clusterOpts.ExtOIDCConfig
				noGroupAuthConfig.TestUsers = noGroupUsername + ":" + noGroupPassword
				noGroupKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &noGroupAuthConfig)
				noGroupAuthClient, err := kauthnv1typedclient.NewForConfig(noGroupKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				noGroupReview, err := noGroupAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "authentication should succeed even when user has no groups")
				t.Logf("✓ User without groups authenticated successfully with groups: %v", noGroupReview.Status.UserInfo.Groups)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test claim validation rules", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test claim validation rules")

				// Refresh admin token before creating test resources
				err := kc.GetAdminToken(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to refresh Keycloak admin token")

				// Setup: Create test resources with automatic cleanup
				testResources := e2eutil.NewTestResources(kc)
				defer testResources.Cleanup(ctx, t)

				// Verify: Claim validation rules are configured
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules).NotTo(BeEmpty())
				claimRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules
				g.Expect(claimRules).Should(HaveLen(2))

				// Verify: Rules are CEL type with expected expressions
				g.Expect(claimRules[0].Type).Should(BeEquivalentTo(configv1.TokenValidationRuleTypeCEL))
				g.Expect(claimRules[0].CEL.Expression).Should(BeEquivalentTo(e2eutil.ClaimValidationExprEmailExists))
				g.Expect(claimRules[1].Type).Should(BeEquivalentTo(configv1.TokenValidationRuleTypeCEL))
				g.Expect(claimRules[1].CEL.Expression).Should(BeEquivalentTo(e2eutil.ClaimValidationExprEmailVerified))

				// Test 1: Valid user - email exists and email_verified=true
				// Should PASS validation and authenticate successfully
				t.Logf("Test 1: Creating user with valid claims (email exists, email_verified=true)")
				validUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "claim-valid-user", "claim-valid-group", clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).NotTo(HaveOccurred(), "authentication must succeed when all claim validation rules pass")
				g.Expect(validUser.Email).NotTo(BeEmpty(), "test user email must be non-empty")
				t.Logf("✓ User with email='%s' and email_verified=true authenticated successfully", validUser.Email)

				// Test 2: Invalid user - email_verified=false
				// Demonstrates rule 2 requirement: claims.email_verified == true
				t.Logf("Test 2: Creating user with email_verified=false (violates rule 2)")
				invalidUsername := "claim-invalid-user-" + e2eutil.GenerateRandomPassword(8)
				invalidEmail := invalidUsername + "@test.example.com"
				invalidPassword := e2eutil.GenerateRandomPassword(16)
				invalidUserID, err := testResources.CreateTestUserWithEmailVerification(ctx, t, invalidUsername, invalidEmail, invalidPassword, false)
				g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
				g.Expect(invalidEmail).NotTo(BeEmpty(), "test user has non-empty email but email_verified=false")
				t.Logf("✓ Created user '%s' with email='%s' and email_verified=false (ID: %s)", invalidUsername, invalidEmail, invalidUserID)

				// Attempt to authenticate - should FAIL due to claim validation rule 2
				err = testResources.TryAuthenticateUser(ctx, t, invalidUsername, invalidPassword, clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).To(HaveOccurred(), "authentication must fail when email_verified=false")
				t.Logf("✓ User with email_verified=false correctly rejected: %v", err)

				// refresh token here so we don't get a 401
				err = kc.GetAdminToken(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to refresh Keycloak admin token")

				// Test 3: Invalid - empty email (violates rule 1)
				// Demonstrates rule 1 requirement: has(claims.email) && claims.email != ''
				t.Logf("Test 3: Creating user with empty email (violates rule 1)")
				emptyEmailUsername := "claim-empty-email-" + e2eutil.GenerateRandomPassword(8)
				emptyEmailPassword := e2eutil.GenerateRandomPassword(16)
				emptyEmailUserID, err := testResources.CreateTestUserWithEmailVerification(ctx, t, emptyEmailUsername, "", emptyEmailPassword, true)
				g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
				t.Logf("✓ Created user '%s' with email='' and email_verified=true (ID: %s)", emptyEmailUsername, emptyEmailUserID)

				// Attempt to authenticate - should FAIL due to claim validation rule 1
				err = testResources.TryAuthenticateUser(ctx, t, emptyEmailUsername, emptyEmailPassword, clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).To(HaveOccurred(), "authentication must fail when email is empty")
				g.Expect(err.Error()).Should(ContainSubstring("Unauthorized"),
					"empty email user cannot authenticate as it violates user validation rule")
				t.Logf("✓ User with empty email correctly rejected: %v", err)

				t.Logf("Claim validation rules successfully validated: only users with non-empty email and email_verified=true can authenticate")
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test user validation rules", func(t *testing.T) {
				g := NewWithT(t)
				t.Logf("begin to test user validation rules")

				// Refresh admin token before creating test resources
				err := kc.GetAdminToken(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to refresh Keycloak admin token")

				// Setup: Create test resources with automatic cleanup
				testResources := e2eutil.NewTestResources(kc)
				defer testResources.Cleanup(ctx, t)

				// Verify: User validation rules are configured
				g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules).NotTo(BeEmpty())
				userRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules
				g.Expect(userRules).Should(HaveLen(2), "should have two user validation rules")

				// Verify: Rules use expected CEL expressions
				expressions := []string{userRules[0].Expression, userRules[1].Expression}
				g.Expect(expressions).Should(ContainElement(e2eutil.UserValidationExprNoSystemPrefix))
				g.Expect(expressions).Should(ContainElement(e2eutil.UserValidationExprNoForbiddenWord))
				t.Logf("User validation rules configured: %v", expressions)

				// Test 1: Valid user - passes all validation rules
				// Should PASS validation and authenticate successfully
				t.Logf("Test 1: Creating user with valid username (no system: prefix, no 'forbidden' word)")
				validUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "user-valid", "user-valid-group", clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).NotTo(HaveOccurred(), "authentication must succeed when all validation rules pass")

				// Username is derived from email via CEL: claims.email.split('@')[0]
				expectedUsername := strings.Split(validUser.Email, "@")[0]
				g.Expect(validUser.SelfSubjectReview.Status.UserInfo.Username).Should(Equal(expectedUsername))
				g.Expect(validUser.SelfSubjectReview.Status.UserInfo.Username).NotTo(HavePrefix("system:"))
				g.Expect(validUser.SelfSubjectReview.Status.UserInfo.Username).NotTo(ContainSubstring("forbidden"))
				t.Logf("✓ User with username='%s' authenticated successfully", validUser.SelfSubjectReview.Status.UserInfo.Username)

				// Test 2: Invalid user - username contains "forbidden"
				// Demonstrates the testable user validation rule: !user.username.contains('forbidden')
				t.Logf("Test 2: Creating user with 'forbidden' in username (violates user validation rule)")
				forbiddenUsername := "user-forbidden-" + e2eutil.GenerateRandomPassword(8)
				forbiddenEmail := forbiddenUsername + "@test.example.com"
				forbiddenPassword := e2eutil.GenerateRandomPassword(16)
				forbiddenUserID, err := testResources.CreateTestUser(ctx, t, forbiddenUsername, forbiddenEmail, forbiddenPassword)
				g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
				t.Logf("Created user with email='%s', mapped username will be '%s' (ID: %s)", forbiddenEmail, forbiddenUsername, forbiddenUserID)

				// Try to authenticate - should FAIL due to user validation rule
				err = testResources.TryAuthenticateUser(ctx, t, forbiddenUsername, forbiddenPassword, clientCfg, clusterOpts.ExtOIDCConfig)
				g.Expect(err).To(HaveOccurred(), "authentication must fail when username contains 'forbidden'")
				g.Expect(err.Error()).Should(ContainSubstring("Unauthorized"),
					"forbidden user cannot authenticate as it violates user validation rule")
				t.Logf("✓ User with 'forbidden' in username correctly rejected with error: %v", err)

				// NOTE: We cannot test the negative case for the system: prefix rule via Keycloak
				// because RFC 5322 email addresses do not allow colons in the local part.
				// Since username = claims.email.split('@')[0], we would need an email like
				// "system:admin@test.example.com", which is invalid per email standards.
				// The system: prefix rule should be tested via unit tests or envtest where claims can be mocked.

				t.Logf("User validation rules successfully validated: users with 'forbidden' in username are rejected")
			})
		}
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}
