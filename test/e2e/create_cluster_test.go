//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestKubeVirtCreateCluster implements a test that mimics the operation described in the
// HyperShift quick start (creating a basic guest cluster).
//
// This test is meant to provide a first, fast signal to detect regression; it
// is recommended to use it as a PR blocker test.
func TestKubeVirtCreateCluster(t *testing.T) {
	// TODO remove this env-var once the Openshift CI lanes
	// move to explicitly opting into the exact tests that should run
	// with the -test.run cli arg.
	if os.Getenv("KUBEVIRT_PLATFORM_ENABLED") != "true" {
		t.Skip("Skipping testing because environment doesn't support KubeVirt")
	}

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Parallel()
	g := NewWithT(t)
	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions()
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.KubevirtPlatform, globalOpts.ArtifactDir)

	waitForHostedClusterAvailable := func() {
		start := time.Now()

		localCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		t.Logf("Waiting for hosted cluster to become available")
		err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
			latest := hostedCluster.DeepCopy()
			err = client.Get(ctx, crclient.ObjectKeyFromObject(latest), latest)
			if err != nil {
				t.Errorf("Failed to get hostedcluster: %v", err)
				return false, nil
			}

			isAvailable := meta.IsStatusConditionTrue(latest.Status.Conditions, string(hyperv1.HostedClusterAvailable))
			if isAvailable {
				return true, nil
			}
			return false, nil
		}, localCtx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for hosted cluster to become available")

		t.Logf("Hosted cluster is available in %s", time.Since(start).Round(time.Second))
	}

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err = client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
	t.Logf("Created nodepool. Namespace: %s, name: %s", nodepool.Namespace, nodepool.Name)

	// Wait for hosted cluster to become ready
	// TODO: replace this with WaitForImageRollout once we can achieve a full
	// image roll out out consistently
	waitForHostedClusterAvailable()

	// Wait for kubevirt machines to come online
	// TODO: Replace this with the generic WaitForNReadyNodes() function
	// once we get better ingress support for KubeVirt platform
	// that allows us to use the guest cluster's client to view
	// node status.
	e2eutil.WaitForKubeVirtMachines(t, testContext, client, hostedCluster, *nodepool.Spec.NodeCount)

	// Wait for kubevirt cluster to be marked as available
	e2eutil.WaitForKubeVirtCluster(t, testContext, client, hostedCluster)

	// TODO verify introspecting guest cluster once ingress is sorted out.
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.ControlPlaneAvailabilityPolicy = "SingleReplica"

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	// Since the None platform has no workers, CVO will not have expectations set,
	// which in turn means that the ClusterVersion object will never be populated.
	// Therefore only test if the control plane comes up (etc, apiserver, ...)
	e2eutil.WaitForConditionsOnHostedControlPlane(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	// etcd restarts for me once always and apiserver two times before running stable
	// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}
