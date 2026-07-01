//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	kauthnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kauthnv1typedclient "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
)

// createTestUserWithGroupAndCustomUsername creates a Keycloak user with a custom username pattern, group, and password.
func createTestUserWithGroup(ctx context.Context, kc *e2eutil.KeycloakAdminClient, usernamePrefix string, emailVerified bool) (string, string, string, string, error) {
	// create group
	groupName := e2eutil.GenerateRandomPassword(16)

	// create user with custom username
	username := e2eutil.GenerateRandomPassword(16)
	if len(usernamePrefix) > 0 {
		username = usernamePrefix + username
	}
	email := username + "@test.example.com"

	password := e2eutil.GenerateRandomPassword(16)

	user := e2eutil.KeycloakUser{
		Username:      username,
		Enabled:       true,
		FirstName:     username,
		LastName:      "Test",
		Email:         email,
		EmailVerified: emailVerified,
		Groups:        []string{groupName},
		Credentials: []e2eutil.KeycloakCredential{
			{
				Type:      "password",
				Value:     password,
				Temporary: false,
			},
		},
	}

	_, err := kc.CreateGroup(ctx, groupName)
	if err != nil {
		return "", "", "", "", err
	}

	_, err = kc.CreateUser(ctx, user)
	if err != nil {
		return "", "", "", "", err
	}

	return username, email, password, groupName, nil
}

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

		// Setup Keycloak admin client
		kc, err := e2eutil.SetupKeycloakAdminClientFromCluster(ctx, t, mgtClient, clusterOpts.ExtOIDCConfig)
		if err != nil {
			t.Skipf("Could not setup Keycloak admin client: %v", err)
		}

		t.Run("[OCPFeatureGate:ExternalOIDC] test keycloak external OIDC", func(t *testing.T) {
			// No gates exist for ExternalOIDC as it has already been enabled by default.
			g := NewWithT(t)
			username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
			g.Expect(err).NotTo(HaveOccurred())

			testAuthConfig := *clusterOpts.ExtOIDCConfig
			testAuthConfig.TestUsers = username + ":" + password
			testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
			testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
			g.Expect(err).NotTo(HaveOccurred())

			selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
			g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
			g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
			g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
			g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

			t.Logf("successfully get oidc user client")

		})

		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
			// ExternalOIDCWithUIDAndExtraClaimMappings has graduated to Default feature set
			// Auth config includes: UID expression + Extra claim mappings
			// Auth config uses: Static claim-based username/groups WITH prefixes (legacy behavior)
			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo username", func(t *testing.T) {
				g := NewWithT(t)
				username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

				t.Logf("begin to test external OIDC with external OIDC userInfo username")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Username).Should(ContainSubstring(clusterOpts.ExtOIDCConfig.UserPrefix))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo Groups", func(t *testing.T) {
				g := NewWithT(t)
				username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

				t.Logf("begin to test external OIDC userInfo Groups")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).Should(ContainElements(ContainSubstring(clusterOpts.ExtOIDCConfig.GroupPrefix)))
			})

			// UID and Extra mappings are present in Default feature set
			// Config: UID expression ("testuid-" + claims.sub + "-uidtest") + 2 Extra mappings
			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo UID", func(t *testing.T) {
				g := NewWithT(t)
				username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

				t.Logf("begin to test external OIDC userInfo UID")
				g.Expect(selfSubjectReview.Status.UserInfo.UID).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(e2eutil.ExternalOIDCUIDExpressionPrefix))
				g.Expect(selfSubjectReview.Status.UserInfo.UID).Should(ContainSubstring(e2eutil.ExternalOIDCUIDExpressionSubfix))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo Extra", func(t *testing.T) {
				g := NewWithT(t)
				username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

				t.Logf("begin to test external OIDC userInfo Extra")
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).NotTo(BeEmpty())
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(e2eutil.ExternalOIDCExtraKeyBar))
				g.Expect(selfSubjectReview.Status.UserInfo.Extra).Should(HaveKey(e2eutil.ExternalOIDCExtraKeyFoo))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC: check co status using oauth client", func(t *testing.T) {
				g := NewWithT(t)
				username, email, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *clusterOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(clusterOpts.ExtOIDCConfig.UserPrefix+email), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", clusterOpts.ExtOIDCConfig.GroupPrefix+groupName))

				t.Logf("begin to test for checking cluster operator status")
				client, err := configv1client.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())
				_, err = client.ConfigV1().ClusterOperators().Get(ctx, "image-registry", metav1.GetOptions{})
				g.Expect(err).To(HaveOccurred())
			})
		}

		// ExternalOIDCWithUpstreamParity tests - Tests CEL expressions and validation rules
		// Auth config adds: CEL expressions for username/groups (NO prefixes)
		// Auth config adds: Claim validation rules (email exists, email_verified)
		// Auth config adds: User validation rules (no system: prefix, no 'forbidden' word)
		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
			upstreamParityOpts := clusterOpts
			upstreamParityOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
			upstreamParityOpts.ExtOIDCConfig.CustomizeAuthSpec = func(spec *configv1.AuthenticationSpec) {
				// Use CEL expression for username mapping instead of static claim
				spec.OIDCProviders[0].ClaimMappings.Username = configv1.UsernameClaimMapping{
					Expression: "claims.email.split('@')[0]",
				}

				// Use CEL expression for groups mapping instead of static claim
				spec.OIDCProviders[0].ClaimMappings.Groups = configv1.PrefixedClaimMapping{
					TokenClaimMapping: configv1.TokenClaimMapping{
						Expression: "claims.?groups.orValue([])",
					},
				}

				// Add claim validation rules
				spec.OIDCProviders[0].ClaimValidationRules = []configv1.TokenClaimValidationRule{
					{
						Type: configv1.TokenValidationRuleTypeCEL,
						CEL: configv1.TokenClaimValidationCELRule{
							Expression: "claims.email_verified == true",
							Message:    "email_verified claim must be true",
						},
					},
				}

				// Add user validation rules
				spec.OIDCProviders[0].UserValidationRules = []configv1.TokenUserValidationRule{
					{
						Expression: "!user.username.contains('forbidden')",
						Message:    "username cannot contain the word 'forbidden'",
					},
				}
			}

			featuregates.ConfigureFeatureSet(upstreamParityOpts.FeatureSet)

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Patch and validate upstream parity config", func(t *testing.T) {
				g := NewWithT(t)

				t.Logf("Patching HostedCluster %s/%s with upstream parity OIDC config", hostedCluster.Namespace, hostedCluster.Name)

				// Build the new auth spec using CustomizeAuthSpec pattern (already exists!)
				// Use upstreamParityOpts which has the CustomizeAuthSpec callback set
				newAuthSpec := upstreamParityOpts.ExtOIDCConfig.GetAuthenticationConfig()

				// Patch using the same pattern as postCreateExternalOIDC
				patchHostedClusterAuth(ctx, g, mgtClient, hostedCluster, newAuthSpec)

				// Wait for KAS to reload - reuse the Eventually pattern from v2 tests
				waitForKASAuthReload(ctx, t, g, clientCfg, upstreamParityOpts.ExtOIDCConfig, kc)

				t.Logf("Successfully patched and validated upstream parity OIDC config")
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Token is valid + authn'd, username/groups mapped correctly", func(t *testing.T) {
				g := NewWithT(t)
				username, _, password, groupName, err := createTestUserWithGroup(ctx, kc, "", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *upstreamParityOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "user should be authenticated + able to do SelfSubjectReview")
				g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(username), "username should be mapped correctly")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(HaveLen(2), "user should have groups system:authenticated and IdP group")
				g.Expect(selfSubjectReview.Status.UserInfo.Groups).To(ContainElements("system:authenticated", groupName))
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Token is valid, user not authn'd, claim validations not passed", func(t *testing.T) {
				g := NewWithT(t)
				username, _, password, _, err := createTestUserWithGroup(ctx, kc, "", false)

				testAuthConfig := *upstreamParityOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				_, err = testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})

				g.Expect(err).To(HaveOccurred(), "user should not be authenticated + able to do SelfSubjectReview")
				g.Expect(apierrors.IsUnauthorized(err)).To(BeTrue(), "should receive an unauthorized error when trying to create SelfSubjectReview")
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Token is valid, user not authn'd, user validations not passed", func(t *testing.T) {
				g := NewWithT(t)
				username, _, password, _, err := createTestUserWithGroup(ctx, kc, "cel-test-user-forbidden", true)
				g.Expect(err).NotTo(HaveOccurred())

				testAuthConfig := *upstreamParityOpts.ExtOIDCConfig
				testAuthConfig.TestUsers = username + ":" + password
				testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
				testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
				g.Expect(err).NotTo(HaveOccurred())

				_, err = testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})

				g.Expect(err).To(HaveOccurred(), "user should not be authenticated + able to do SelfSubjectReview")
				g.Expect(apierrors.IsUnauthorized(err)).To(BeTrue(), "should receive an unauthorized error when trying to create SelfSubjectReview")
			})
		}
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}

// patchHostedClusterAuth patches the HostedCluster's authentication configuration
// Reuses the exact same pattern as azure.go:postCreateExternalOIDC (lines 305-313)
func patchHostedClusterAuth(ctx context.Context, g Gomega, mgtClient crclient.Client, hc *hyperv1.HostedCluster, newAuthSpec *configv1.AuthenticationSpec) {
	// Get the latest version
	current := &hyperv1.HostedCluster{}
	err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Name}, current)
	g.Expect(err).NotTo(HaveOccurred(), "should be able to get HostedCluster")

	// Create patch
	patch := crclient.MergeFrom(current.DeepCopy())

	// Mutate
	if current.Spec.Configuration == nil {
		current.Spec.Configuration = &hyperv1.ClusterConfiguration{}
	}
	current.Spec.Configuration.Authentication = newAuthSpec

	// Apply
	err = mgtClient.Patch(ctx, current, patch)
	g.Expect(err).NotTo(HaveOccurred(), "should be able to patch HostedCluster authentication config")
}

// waitForKASAuthReload waits for KAS to pick up the new authentication config
// Reuses the Eventually pattern from hosted_cluster_external_oidc_test.go:310-325
func waitForKASAuthReload(ctx context.Context, t *testing.T, g Gomega, clientCfg *rest.Config, authConfig *e2eutil.ExtOIDCConfig, kc *e2eutil.KeycloakAdminClient) {
	t.Logf("Waiting for KAS to reload authentication config (timeout: 5 minutes)")

	// Create a test user to validate the new config
	username, _, password, _, err := createTestUserWithGroup(ctx, kc, "", true)
	g.Expect(err).NotTo(HaveOccurred())

	testAuthConfig := *authConfig
	testAuthConfig.TestUsers = username + ":" + password

	// This is the SAME pattern as v2 tests - Eventually + fresh token per attempt
	g.Eventually(func(g Gomega) {
		t.Logf("Attempting authentication with new OIDC config (user: %s)", username)

		// Get fresh token each attempt (Keycloak tokens have short TTL)
		testUserKubeConfig := e2eutil.ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
		testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
		g.Expect(err).NotTo(HaveOccurred())

		// Try SelfSubjectReview - fails until KAS reloads
		selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
		if err != nil {
			t.Logf("Authentication attempt failed (expected during reload): %v", err)
		}
		g.Expect(err).NotTo(HaveOccurred(), "KAS should accept OIDC token after reload")

		// Verify new config is active (username = email local part, NO prefix)
		g.Expect(selfSubjectReview.Status.UserInfo.Username).To(Equal(username), "username should use CEL expression (no prefix)")

		t.Logf("KAS has successfully reloaded authentication config")
	}).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
}
