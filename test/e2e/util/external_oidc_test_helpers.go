package util

import (
	"context"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	kauthnv1 "k8s.io/api/authentication/v1"
	"k8s.io/client-go/rest"
)

// TestCELUsernameMapping validates that CEL username expression correctly maps email to username.
// When it should use CEL username expression mapping
func TestCELUsernameMapping(t *testing.T, ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	kc *KeycloakAdminClient,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig) {

	g := NewWithT(t)
	t.Logf("begin to test CEL username expression mapping")

	// Setup: Create test resources with automatic cleanup
	testResources, err := NewTestResources(ctx, kc, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred())
	defer testResources.Cleanup(ctx, t)

	// Setup: Create authenticated test user with group
	testUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "cel-test-user", "cel-test-group", clientCfg, extOIDCConfig)
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
	preferredUsername := "cel-preferred-" + GenerateRandomPassword(8)
	actualEmail := "cel-email-" + GenerateRandomPassword(8) + "@test.example.com"
	preferredPassword := GenerateRandomPassword(16)

	// In Keycloak, the 'username' field becomes the 'preferred_username' claim
	// So we create a user where username != email local part
	_, err = testResources.CreateTestUser(ctx, t, preferredUsername, actualEmail, preferredPassword)
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Created user: preferred_username='%s', email='%s'", preferredUsername, actualEmail)

	// Authenticate as this user
	preferredAuthConfig := *extOIDCConfig
	preferredAuthConfig.TestUsers = preferredUsername + ":" + preferredPassword
	preferredReview, err := testResources.AuthenticateAndGetSelfSubjectReview(ctx, t, preferredUsername, preferredPassword, clientCfg, &preferredAuthConfig)
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
		g.Expect(group).NotTo(HavePrefix(extOIDCConfig.GroupPrefix),
			"groups should not have prefix when using CEL expression")
	}
	g.Expect(hasTestGroup).To(BeTrue(), "user should be member of test group: %s", testUser.GroupName)
	t.Logf("CEL groups expression correctly mapped groups: %v", testUser.SelfSubjectReview.Status.UserInfo.Groups)
}

// TestCELGroupsMapping validates that CEL groups expression correctly maps groups without prefix.
// When it should test CEL groups expression mapping
func TestCELGroupsMapping(t *testing.T, ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	kc *KeycloakAdminClient,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig) {

	g := NewWithT(t)
	t.Logf("begin to test CEL groups expression mapping")

	// Setup: Create test resources with automatic cleanup
	testResources, err := NewTestResources(ctx, kc, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred())
	defer testResources.Cleanup(ctx, t)

	// Verify: Groups expression is configured
	g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression).NotTo(BeEmpty())
	t.Logf("CEL groups expression configured: %s", hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Groups.Expression)

	// Setup: Create authenticated test user with group
	testUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "cel-groups-test-user", "cel-groups-test-group", clientCfg, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify: User has groups from Keycloak
	g.Expect(testUser.SelfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty(),
		"user should have groups from Keycloak")

	// Verify: Groups are mapped without prefix (CEL expression removes prefix)
	for _, group := range testUser.SelfSubjectReview.Status.UserInfo.Groups {
		g.Expect(group).NotTo(HavePrefix(extOIDCConfig.GroupPrefix),
			"groups should not have prefix when using CEL expression")
	}

	// Verify: User is member of the test group
	hasTestGroup := slices.Contains(testUser.SelfSubjectReview.Status.UserInfo.Groups, testUser.GroupName)
	g.Expect(hasTestGroup).To(BeTrue(), "user should be member of test group: %s", testUser.GroupName)
	t.Logf("CEL groups expression successfully mapped groups without prefix: %v", testUser.SelfSubjectReview.Status.UserInfo.Groups)

	// Negative test: User without groups should still authenticate
	// The CEL expression claims.?groups.orValue([]) handles missing groups claim gracefully
	t.Logf("Negative test: Creating user without group membership")
	noGroupUsername := "cel-no-groups-" + GenerateRandomPassword(8)
	noGroupEmail := noGroupUsername + "@test.example.com"
	noGroupPassword := GenerateRandomPassword(16)
	_, err = testResources.CreateTestUser(ctx, t, noGroupUsername, noGroupEmail, noGroupPassword)
	g.Expect(err).NotTo(HaveOccurred())

	// Authenticate user without groups - should SUCCEED with empty groups
	noGroupReview, err := testResources.AuthenticateAndGetSelfSubjectReview(ctx, t, noGroupUsername, noGroupPassword, clientCfg, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred(), "authentication should succeed even when user has no groups")
	t.Logf("✓ User without groups authenticated successfully with groups: %v", noGroupReview.Status.UserInfo.Groups)
}

// TestClaimValidationRules validates that claim validation rules reject invalid tokens.
// When it should test claim validation rules
func TestClaimValidationRules(t *testing.T, ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	kc *KeycloakAdminClient,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig) {

	g := NewWithT(t)
	t.Logf("begin to test claim validation rules")

	// Setup: Create test resources with automatic cleanup
	testResources, err := NewTestResources(ctx, kc, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred())
	defer testResources.Cleanup(ctx, t)

	// Verify: Claim validation rules are configured
	g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules).NotTo(BeEmpty())
	claimRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimValidationRules
	g.Expect(claimRules).Should(HaveLen(2))

	// Verify: Rules are CEL type with expected expressions
	g.Expect(claimRules[0].Type).Should(BeEquivalentTo("CEL"))
	g.Expect(claimRules[0].CEL.Expression).Should(BeEquivalentTo(ClaimValidationExprEmailExists))
	g.Expect(claimRules[1].Type).Should(BeEquivalentTo("CEL"))
	g.Expect(claimRules[1].CEL.Expression).Should(BeEquivalentTo(ClaimValidationExprEmailVerified))

	// Test 1: Valid user - email exists and email_verified=true
	// Should PASS validation and authenticate successfully
	t.Logf("Test 1: Creating user with valid claims (email exists, email_verified=true)")
	validUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "claim-valid-user", "claim-valid-group", clientCfg, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred(), "authentication must succeed when all claim validation rules pass")
	g.Expect(validUser.Email).NotTo(BeEmpty(), "test user email must be non-empty")
	t.Logf("✓ User with email='%s' and email_verified=true authenticated successfully", validUser.Email)

	// Test 2: Invalid user - email_verified=false
	// Demonstrates rule 2 requirement: claims.email_verified == true
	t.Logf("Test 2: Creating user with email_verified=false (violates rule 2)")
	invalidUsername := "claim-invalid-user-" + GenerateRandomPassword(8)
	invalidEmail := invalidUsername + "@test.example.com"
	invalidPassword := GenerateRandomPassword(16)
	invalidUserID, err := testResources.CreateTestUserWithEmailVerification(ctx, t, invalidUsername, invalidEmail, invalidPassword, false)
	g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
	g.Expect(invalidEmail).NotTo(BeEmpty(), "test user has non-empty email but email_verified=false")
	t.Logf("✓ Created user '%s' with email='%s' and email_verified=false (ID: %s)", invalidUsername, invalidEmail, invalidUserID)

	// Attempt to authenticate - should FAIL due to claim validation rule 2
	err = testResources.TryAuthenticateUser(ctx, t, invalidUsername, invalidPassword, clientCfg, extOIDCConfig)
	g.Expect(err).To(HaveOccurred(), "authentication must fail when email_verified=false")
	t.Logf("✓ User with email_verified=false correctly rejected: %v", err)

	// Test 3: Invalid - empty email (violates rule 1)
	// Demonstrates rule 1 requirement: has(claims.email) && claims.email != ''
	t.Logf("Test 3: Creating user with empty email (violates rule 1)")
	emptyEmailUsername := "claim-empty-email-" + GenerateRandomPassword(8)
	emptyEmailPassword := GenerateRandomPassword(16)
	emptyEmailUserID, err := testResources.CreateTestUserWithEmailVerification(ctx, t, emptyEmailUsername, "", emptyEmailPassword, true)
	g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
	t.Logf("✓ Created user '%s' with email='' and email_verified=true (ID: %s)", emptyEmailUsername, emptyEmailUserID)

	// Attempt to authenticate - should FAIL due to claim validation rule 1
	err = testResources.TryAuthenticateUser(ctx, t, emptyEmailUsername, emptyEmailPassword, clientCfg, extOIDCConfig)
	g.Expect(err).To(HaveOccurred(), "authentication must fail when email is empty")
	g.Expect(err.Error()).Should(ContainSubstring("Unauthorized"),
		"empty email user cannot authenticate as it violates user validation rule")
	t.Logf("✓ User with empty email correctly rejected: %v", err)

	t.Logf("Claim validation rules successfully validated: only users with non-empty email and email_verified=true can authenticate")
}

// TestUserValidationRules validates that user validation rules reject invalid usernames.
// When it should test user validation rules
func TestUserValidationRules(t *testing.T, ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	kc *KeycloakAdminClient,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig) {

	g := NewWithT(t)
	t.Logf("begin to test user validation rules")

	// Setup: Create test resources with automatic cleanup
	testResources, err := NewTestResources(ctx, kc, extOIDCConfig)
	g.Expect(err).NotTo(HaveOccurred())
	defer testResources.Cleanup(ctx, t)

	// Verify: User validation rules are configured
	g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules).NotTo(BeEmpty())
	userRules := hostedCluster.Spec.Configuration.Authentication.OIDCProviders[0].UserValidationRules
	g.Expect(userRules).Should(HaveLen(2), "should have two user validation rules")

	// Verify: Rules use expected CEL expressions
	expressions := []string{userRules[0].Expression, userRules[1].Expression}
	g.Expect(expressions).Should(ContainElement(UserValidationExprNoSystemPrefix))
	g.Expect(expressions).Should(ContainElement(UserValidationExprNoForbiddenWord))
	t.Logf("User validation rules configured: %v", expressions)

	// Test 1: Valid user - passes all validation rules
	// Should PASS validation and authenticate successfully
	t.Logf("Test 1: Creating user with valid username (no system: prefix, no 'forbidden' word)")
	validUser, err := testResources.SetupAuthenticatedUserWithGroup(ctx, t, "user-valid", "user-valid-group", clientCfg, extOIDCConfig)
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
	forbiddenUsername := "user-forbidden-" + GenerateRandomPassword(8)
	forbiddenEmail := forbiddenUsername + "@test.example.com"
	forbiddenPassword := GenerateRandomPassword(16)
	forbiddenUserID, err := testResources.CreateTestUser(ctx, t, forbiddenUsername, forbiddenEmail, forbiddenPassword)
	g.Expect(err).NotTo(HaveOccurred(), "creating user in Keycloak should succeed")
	t.Logf("Created user with email='%s', mapped username will be '%s' (ID: %s)", forbiddenEmail, forbiddenUsername, forbiddenUserID)

	// Try to authenticate - should FAIL due to user validation rule
	err = testResources.TryAuthenticateUser(ctx, t, forbiddenUsername, forbiddenPassword, clientCfg, extOIDCConfig)
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
}

// TestLegacyPrefixedMappings validates username/group prefix behavior from the legacy feature gate.
// When it should test legacy prefixed mappings (ExternalOIDCWithUIDAndExtraClaimMappings without ExternalOIDCWithUpstreamParity)
func TestLegacyPrefixedMappings(t *testing.T, ctx context.Context,
	hostedCluster *hyperv1.HostedCluster,
	kc *KeycloakAdminClient,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig,
	selfSubjectReview *kauthnv1.SelfSubjectReview) {

	g := NewWithT(t)
	t.Logf("begin to test external OIDC with external OIDC userInfo username")
	g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(BeEmpty())
	g.Expect(selfSubjectReview.Status.UserInfo.Username).Should(ContainSubstring(extOIDCConfig.UserPrefix))

	t.Logf("begin to test external OIDC userInfo Groups")
	g.Expect(selfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty())
	g.Expect(selfSubjectReview.Status.UserInfo.Groups).Should(ContainElements(ContainSubstring(extOIDCConfig.GroupPrefix)))
}

// TestUIDAndExtraMappings validates UID expression and extra claim mappings.
// When it should test UID and extra claim mappings (ExternalOIDCWithUIDAndExtraClaimMappings)
func TestUIDAndExtraMappings(t *testing.T, ctx context.Context, selfSubjectReview *kauthnv1.SelfSubjectReview) {
	g := NewWithT(t)
	t.Logf("begin to test external OIDC userInfo UID")
	g.Expect(selfSubjectReview.Status.UserInfo.UID).NotTo(BeEmpty())
	g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(ExternalOIDCUIDExpressionPrefix))
	g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(ExternalOIDCUIDExpressionSubfix))

	t.Logf("begin to test external OIDC userInfo Extra")
	g.Expect(selfSubjectReview.Status.UserInfo.Extra).NotTo(BeEmpty())
	g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(ExternalOIDCExtraKeyBar))
	g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(ExternalOIDCExtraKeyFoo))
}
