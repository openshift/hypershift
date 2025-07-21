//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestExternalOIDC(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)
	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") != "true" {
		t.Skipf("Only tested when CI sets TECH_PREVIEW_NO_UPGRADE=true and the Hypershift Operator is installed with --tech-preview-no-upgrade")
	}

	if !globalOpts.EnableExternalOIDC {
		t.Skipf("skip external OIDC test if e2e e2e.enable-external-oidc is false")
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("Check external OIDC spec", func(t *testing.T) {
			g := NewWithT(t)
			t.Logf("begin to run external OIDC cases")
			g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
			g.Expect(hostedCluster.Spec.Configuration.Authentication.OIDCProviders).NotTo(BeEmpty())
			t.Logf("%+v", hostedCluster)
			t.Log("begin to sleep the cluster")
			time.Sleep(time.Minute * 10)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "externaloidc", globalOpts.ServiceAccountSigningKey)

}
