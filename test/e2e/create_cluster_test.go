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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1 "github.com/openshift/api/operator/v1"
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

	ctx, cancel := context.WithTimeout(testContext, 35*time.Minute)
	defer cancel()

	t.Parallel()
	g := NewWithT(t)
	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// get base domain from default ingress of management cluster
	defaultIngressOperator := &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "openshift-ingress-operator",
		},
	}
	err = client.Get(ctx, crclient.ObjectKeyFromObject(defaultIngressOperator), defaultIngressOperator)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get default '*.apps' ingress of management cluster")

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.BaseDomain = defaultIngressOperator.Status.Domain

	t.Logf("Using base domain %s", clusterOpts.BaseDomain)
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.KubevirtPlatform, globalOpts.ArtifactDir)

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
	t.Logf("Created nodepool. Namespace: %s, name: %s", nodepool.Namespace, nodepool.Name)

	t.Logf("Waiting for KubeVirtMachines to be marked as ready")
	e2eutil.WaitForKubeVirtMachines(t, ctx, client, hostedCluster, *nodepool.Spec.NodeCount)

	// Wait for kubevirt cluster to be marked as available
	t.Logf("Waiting for KubeVirtCluster to be marked as ready")
	e2eutil.WaitForKubeVirtCluster(t, ctx, client, hostedCluster)

	// Get a client for the cluster
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Using the guest client, introspect that the nodes for the tenant cluster become ready
	t.Logf("Waiting for nodes to become ready")
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, *nodepool.Spec.NodeCount)

	// Setup wildcard *.apps route for nested kubevirt cluster
	t.Logf("Setting up wildcard *.apps route for nested kubevirt tenant cluster")
	e2eutil.CreateKubeVirtClusterWildcardRoute(t, ctx, client, guestClient, hostedCluster, clusterOpts.BaseDomain)

	// Verify the cluster rolls out completely
	t.Logf("Waiting for cluster operators to become available")
	e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, globalOpts.LatestReleaseImage)
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
