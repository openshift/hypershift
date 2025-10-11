//go:build e2e
// +build e2e

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
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}

func TestExternalOIDCTechPreview(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)
	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") != "true" {
		t.Skipf("Only tested when CI sets TECH_PREVIEW_NO_UPGRADE=true and the Hypershift Operator is installed with --tech-preview-no-upgrade")
	}

	if globalOpts.ExternalOIDCProvider == "" {
		t.Skipf("skip external OIDC test if e2e.external-oidc-provider is not provided")
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
	clusterOpts.NodePoolReplicas = 1

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Logf("begin to test external OIDC with TechPreviewNoUpgrade enabled %s", globalOpts.ExternalOIDCProvider)
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
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}
