//go:build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCiliumConnectivity validates Cilium connectivity using the official Cilium connectivity test suite.
// This test is specifically for ARO HCP clusters with Cilium as the network provider.
//
// The test performs the following steps:
// 1.Check cilium agent pods ready
// 2. Creates cilium-test namespace with appropriate labels
// 3. Deploys Cilium connectivity test pods
// 4. Waits for all test pods to be ready and running
// 5. Cleans up test resources.

func TestCiliumConnectivity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("Skipping test because it requires Azure platform")
	}

	if globalOpts.ExternalCNIProvider != e2eutil.CiliumCNIProvider {
		t.Skipf("skip cilium connection test if e2e.external-cni-provider is not %s", e2eutil.CiliumCNIProvider)
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		if !azureutil.IsAroHCP() {
			t.Skip("test only supported on ARO HCP clusters")
		}
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		cleanup := e2eutil.EnsureCiliumConnectivityTestResources(t, ctx, guestClient, content.ReadFile)
		defer cleanup()
	}).WithAssetReader(content.ReadFile).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "cilium-connectivity", globalOpts.ServiceAccountSigningKey)
}
