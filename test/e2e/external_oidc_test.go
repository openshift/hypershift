//go:build e2e

package e2e

import (
	"context"
	"os"
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

// TestExternalOIDC validates external OIDC authentication across feature gate configurations.
//
// Feature Gate Evolution:
//   - ExternalOIDC: Base OIDC support (graduated to Default)
//   - ExternalOIDCWithUIDAndExtraClaimMappings: Adds UID expression + Extra mappings (graduated to Default)
//   - ExternalOIDCWithUpstreamParity: Adds CEL expressions, validation rules (currently TechPreviewNoUpgrade)
//
// Test Execution Matrix:
//
// 1. Main test execution (lines 49-120):
//   - Always runs with feature-gate-driven auth config
//   - Default feature set: Static claim mappings with prefixes + UID/Extra
//   - TechPreviewNoUpgrade: CEL expressions (no prefixes) + validation rules + UID/Extra
//
// 2. Custom config test execution (lines 122-154):
//   - Only runs when ExternalOIDCWithUpstreamParity enabled
//   - Uses CustomizeAuthSpec to override UID (claim) and Extra (1 mapping)
//   - Preserves CEL expressions and validation rules from feature gate
//   - Demonstrates configuration customization for testing variants
//
// Auth Config by Feature Set:
//
// Default:
//
//	Username: Static claim "email" WITH prefix
//	Groups:   Static claim "groups" WITH prefix
//	UID:      Expression ("testuid-" + claims.sub + "-uidtest")
//	Extra:    2 mappings (bar, foo)
//
// TechPreviewNoUpgrade:
//
//	Username: CEL expression (claims.email.split('@')[0]) - NO prefix
//	Groups:   CEL expression (claims.?groups.orValue([])) - NO prefix
//	UID:      Expression (same as Default)
//	Extra:    2 mappings (same as Default)
//	ClaimValidationRules:  email exists, email_verified
//	UserValidationRules:   no system: prefix, no 'forbidden' word
//
// Custom Config (TechPreviewNoUpgrade + CustomizeAuthSpec):
//
//	Username: CEL expression (preserved from feature gate)
//	Groups:   CEL expression (preserved from feature gate)
//	UID:      Claim "sub" (OVERRIDDEN by CustomizeAuthSpec)
//	Extra:    1 mapping (REPLACED by CustomizeAuthSpec)
//	ClaimValidationRules:  (preserved from feature gate)
//	UserValidationRules:   (preserved from feature gate)
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

		// ExternalOIDCWithUIDAndExtraClaimMappings has graduated to Default feature set
		// Auth config includes: UID expression + Extra claim mappings
		// Auth config uses: Static claim-based username/groups WITH prefixes (legacy behavior)

		// Legacy prefixed mappings test only runs when ExternalOIDCWithUpstreamParity is NOT enabled
		// Config: Username/Groups use static claims with prefixes (claim: "email", prefix: "prefix-")
		// When ExternalOIDCWithUpstreamParity IS enabled, it overwrites with CEL expressions (no prefixes)
		if !featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
			t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo username and groups", func(t *testing.T) {
				e2eutil.TestLegacyPrefixedMappings(t, ctx, hostedCluster, kc, clientCfg, clusterOpts.ExtOIDCConfig, selfSubjectReview)
			})
		}

		// UID and Extra mappings are present in Default feature set
		// Config: UID expression ("testuid-" + claims.sub + "-uidtest") + 2 Extra mappings
		t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC userInfo UID and Extra", func(t *testing.T) {
			e2eutil.TestUIDAndExtraMappings(t, ctx, selfSubjectReview)
		})

		t.Run("[OCPFeatureGate:ExternalOIDCWithUIDAndExtraClaimMappings] Test external OIDC: check co status using oauth client", func(t *testing.T) {
			g := NewWithT(t)
			t.Logf("begin to test for checking co status")
			client, err := configv1client.NewForConfig(authKubeConfig)
			g.Expect(err).NotTo(HaveOccurred())
			_, err = client.ConfigV1().ClusterOperators().Get(ctx, "image-registry", metav1.GetOptions{})
			g.Expect(err).To(HaveOccurred())
		})

		// ExternalOIDCWithUpstreamParity is currently TechPreviewNoUpgrade only
		// Auth config adds: CEL expressions for username/groups (NO prefixes)
		// Auth config adds: Claim validation rules (email exists, email_verified)
		// Auth config adds: User validation rules (no system: prefix, no 'forbidden' word)
		if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL username expression mapping", func(t *testing.T) {
				e2eutil.TestCELUsernameMapping(t, ctx, hostedCluster, kc, clientCfg, clusterOpts.ExtOIDCConfig)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test CEL groups expression mapping", func(t *testing.T) {
				e2eutil.TestCELGroupsMapping(t, ctx, hostedCluster, kc, clientCfg, clusterOpts.ExtOIDCConfig)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test claim validation rules", func(t *testing.T) {
				e2eutil.TestClaimValidationRules(t, ctx, hostedCluster, kc, clientCfg, clusterOpts.ExtOIDCConfig)
			})

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test user validation rules", func(t *testing.T) {
				e2eutil.TestUserValidationRules(t, ctx, hostedCluster, kc, clientCfg, clusterOpts.ExtOIDCConfig)
			})
		}
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)

	// Custom config test - Tests configuration customization via CustomizeAuthSpec
	// Demonstrates that CustomizeAuthSpec can override specific fields while keeping others
	// Base config from ExternalOIDCWithUpstreamParity feature gate includes:
	//   - CEL username/groups expressions
	//   - Claim validation rules
	//   - User validation rules
	// CustomizeAuthSpec OVERRIDES:
	//   - UID: Changes from expression to claim-based (Claim: "sub")
	//   - Extra: Reduces from 2 mappings to 1 mapping
	// CustomizeAuthSpec PRESERVES (does not touch):
	//   - Username/Groups CEL expressions
	//   - Claim validation rules (still present, that's why TestUserValidationRules works)
	//   - User validation rules (still present, that's why TestUserValidationRules works)
	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
		customClusterOpts := clusterOpts
		customClusterOpts.ExtOIDCConfig.CustomizeAuthSpec = e2eutil.AuthConfigCombined(
			e2eutil.AuthConfigUIDFromClaim(),
			e2eutil.AuthConfigMinimalExtraMappings(),
		)

		e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
			g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty())
			clientCfg := e2eutil.WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)

			// Setup Keycloak admin client
			kc, err := e2eutil.SetupKeycloakAdminClientFromCluster(ctx, t, mgtClient, customClusterOpts.ExtOIDCConfig)
			if err != nil {
				t.Skipf("Could not setup Keycloak admin client: %v", err)
			}

			t.Run("[OCPFeatureGate:ExternalOIDCWithUpstreamParity] Test user validation rules (custom UID config)", func(t *testing.T) {
				// This test works because validation rules are preserved from feature gate config
				// Only UID and Extra mappings were customized
				e2eutil.TestUserValidationRules(t, ctx, hostedCluster, kc, clientCfg, customClusterOpts.ExtOIDCConfig)
			})
		}).Execute(&customClusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ext-oidc-uid-claim", globalOpts.ServiceAccountSigningKey)
	}

}
