//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client := e2eutil.GetClientOrDie()

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.ControlPlaneAvailabilityPolicy = "SingleReplica"

	hostedCluster := e2eutil.CreateNoneCluster(t, ctx, client, clusterOpts, globalOpts.ArtifactDir)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	// Since the None platform has no workers, CVO will not have expectations set,
	// which in turn means that the ClusterVersion object will never be populated.
	// Therefore only test if the control plane comes up (etc, apiserver, ...)
	e2eutil.WaitForConditionsOnHostedControlPlane(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	// etcd restarts for me once always and apiserver two times before running stable
	// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
