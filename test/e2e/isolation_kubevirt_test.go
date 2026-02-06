//go:build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestKubevirtKernelLevelIsolation validates that KubeVirt provides kernel-level isolation
// by verifying that control plane components run in VMs with separate kernel instances
func TestKubevirtKernelLevelIsolation(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("Kernel-level isolation test requires KubeVirt platform")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// Create test with validation function
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Logf("Starting kernel-level isolation validation for cluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)

		// Validate kernel-level isolation by comparing management and guest cluster kernels
		e2eutil.EnsureKernelLevelIsolation(t, ctx, mgtClient, hostedCluster)

		// Validate NetworkPolicy enforcement at VM level
		e2eutil.EnsureVMLauncherNetworkPolicies(t, ctx, mgtClient, hostedCluster)

		t.Logf("✓ KERNEL-LEVEL ISOLATION VALIDATED for cluster %s", hostedCluster.Name)
		t.Logf("  Evidence:")
		t.Logf("  - Separate kernel instances confirmed between management and guest clusters")
		t.Logf("  - VirtLauncher NetworkPolicy enforced")
		t.Logf("  - VM-based isolation via KubeVirt platform")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "kernel-isolation", globalOpts.ServiceAccountSigningKey)
}
