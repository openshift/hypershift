//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReplaceUpgradeNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting NodePool replace upgrade test from %s to %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.LatestReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch v := o.(type) {
		case *hyperv1.NodePool:
			v.Spec.Release.Image = globalOpts.PreviousReleaseImage
			v.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
				Strategy: hyperv1.UpgradeStrategyRollingUpdate,
				RollingUpdate: &hyperv1.RollingUpdate{
					MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
					MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(3)),
				},
			}
		}
	}

	// Look up metadata about the release images so that we can extract the version
	// information for later assertions.
	releaseInfoProvider := &releaseinfo.RegistryClientProvider{}
	pullSecretFile, err := os.Open(clusterOpts.PullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to open pull secret file")
	defer pullSecretFile.Close()
	pullSecret, err := ioutil.ReadAll(pullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to read pull secret file")
	previousReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.PreviousReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for previous image")
	latestReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")

	// Create the test cluster.
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.AWSPlatform, globalOpts.ArtifactDir)

	// Wait for connectivity to the cluster.
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready.
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the first rollout to be complete and refresh the HostedCluster.
	t.Logf("Waiting for initial cluster rollout. Image: %s", hostedCluster.Spec.Release.Image)
	e2eutil.WaitForImageRollout(t, ctx, client, guestClient, hostedCluster, hostedCluster.Spec.Release.Image)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Find NodePools.
	var nodePools hyperv1.NodePoolList
	err = client.List(ctx, &nodePools, &crclient.ListOptions{Namespace: hostedCluster.Namespace})
	g.Expect(err).NotTo(HaveOccurred(), "failed to list NodePools")

	// Wait for NodePools to roll out the initial version.
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, previousReleaseInfo.Version())
	}

	// Update NodePool images to the latest.
	for _, nodePool := range nodePools.Items {
		err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
		t.Logf("Updating NodePool image. Image: %s", globalOpts.LatestReleaseImage)
		original := nodePool.DeepCopy()
		nodePool.Spec.Release.Image = globalOpts.LatestReleaseImage
		err = client.Patch(ctx, &nodePool, crclient.MergeFrom(original))
		g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool image")
	}

	// Check the upgrade is signalled in a condition.
	for _, nodePool := range nodePools.Items {
		err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
			err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

			for _, condition := range nodePool.Status.Conditions {
				if condition.Type == hyperv1.NodePoolUpdatingVersionConditionType && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to find UpdatingVersionCondition condition")
		t.Log("NodePool have UpdatingVersionCondition condition")
	}

	// Wait for at least 1 Node to be unready, so we know the process is started.
	e2eutil.WaitForNUnReadyNodes(t, ctx, guestClient, 1)
	t.Log("Upgrade has stated as at least 1 Node to is unready")
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for NodePools to roll out the latest version
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, latestReleaseInfo.Version())
		e2eutil.WaitForNodePoolConditionsNotToBePresent(t, ctx, client, &nodePool, hyperv1.NodePoolUpdatingVersionConditionType)
	}

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Namespace)
}

func TestInPlaceUpgradeNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting NodePool in place upgrade test from %s to %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.LatestReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch v := o.(type) {
		case *hyperv1.NodePool:
			v.Spec.Release.Image = globalOpts.PreviousReleaseImage
			v.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		}
	}

	// Look up metadata about the release images so that we can extract the version
	// information for later assertions
	releaseInfoProvider := &releaseinfo.RegistryClientProvider{}
	pullSecretFile, err := os.Open(clusterOpts.PullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to open pull secret file")
	defer pullSecretFile.Close()
	pullSecret, err := ioutil.ReadAll(pullSecretFile)
	g.Expect(err).NotTo(HaveOccurred(), "failed to read pull secret file")
	previousReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.PreviousReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for previous image")
	latestReleaseInfo, err := releaseInfoProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get release info for latest image")

	// Create the test cluster
	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Wait for connectivity to the cluster
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the first rollout to be complete and refresh the hostedcluster
	t.Logf("Waiting for initial cluster rollout. Image: %s", hostedCluster.Spec.Release.Image)
	e2eutil.WaitForImageRollout(t, ctx, client, guestClient, hostedCluster, hostedCluster.Spec.Release.Image)
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Find nodepools
	var nodePools hyperv1.NodePoolList
	err = client.List(ctx, &nodePools, &crclient.ListOptions{Namespace: hostedCluster.Namespace})
	g.Expect(err).NotTo(HaveOccurred(), "failed to list NodePools")

	// Wait for nodepools to roll out the initial version
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, previousReleaseInfo.Version())
	}

	// Update NodePool images to the latest
	for _, nodePool := range nodePools.Items {
		err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
		t.Logf("Updating NodePool image. Image: %s", globalOpts.LatestReleaseImage)
		original := nodePool.DeepCopy()
		nodePool.Spec.Release.Image = globalOpts.LatestReleaseImage
		err = client.Patch(ctx, &nodePool, crclient.MergeFrom(original))
		g.Expect(err).NotTo(HaveOccurred(), "failed update nodePool image")
	}

	// Check the upgrade is signalled in a condition.
	for _, nodePool := range nodePools.Items {
		err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
			err = client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")

			for _, condition := range nodePool.Status.Conditions {
				if condition.Type == hyperv1.NodePoolUpdatingVersionConditionType && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to find UpdatingVersionCondition condition")
		t.Log("NodePool have UpdatingVersionCondition condition")
	}

	// Wait for at least 1 Node to be unready, so we know the process is started.
	e2eutil.WaitForNUnReadyNodes(t, ctx, guestClient, 1)
	t.Log("Upgrade has started as at least 1 Node to is unready")
	e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for NodePools to roll out the latest version.
	// TODO: Consider doing this in parallel
	for _, nodePool := range nodePools.Items {
		e2eutil.WaitForNodePoolVersion(t, ctx, client, &nodePool, latestReleaseInfo.Version())
		e2eutil.WaitForNodePoolConditionsNotToBePresent(t, ctx, client, &nodePool, hyperv1.NodePoolUpdatingVersionConditionType)
	}

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Namespace)
}
