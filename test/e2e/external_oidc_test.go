//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func TestExternalOIDC(t *testing.T) {
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

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("[OCPFeatureGate:ExternalOIDC] test keycloak external OIDC", func(t *testing.T) {
			g := NewWithT(t)
			t.Logf("begin to test external OIDC %s", globalOpts.ExternalOIDCProvider)
			g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty())
			clientCfg := e2eutil.WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)
			e2eutil.ChangeClientForKeycloakExtOIDC(t, ctx, clientCfg, clusterOpts.ExtOIDCConfig)
			t.Logf("successfully get oidc user client")
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "external-oidc", globalOpts.ServiceAccountSigningKey)
}
