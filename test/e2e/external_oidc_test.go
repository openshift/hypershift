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
