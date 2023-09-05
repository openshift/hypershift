//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/conditions"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

var (
	zeroReplicas int32 = 0
	oneReplicas  int32 = 1
	twoReplicas  int32 = 2
)

type NodePoolTestCase struct {
	name            string
	test            NodePoolTest
	manifestBuilder NodePoolManifestBuilder
}

func TestNodePool(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// We set replicas to 0 in order to allow the inner tests to
	// create their own NodePools with the proper replicas
	clusterOpts.NodePoolReplicas = 0
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		hostedClusterClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Get the newly created defautlt NodePool
		nodepools := &hyperv1.NodePoolList{}
		if err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
			t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
		}
		g.Expect(nodepools.Items).ToNot(BeEmpty())
		defaultNodepool := &nodepools.Items[0]

		// Set of tests
		// Each test should have their own NodePool
		nodePoolTests := []NodePoolTestCase{

			{
				name: "TestKMSRootVolumeEncryption",
				test: NewKMSRootVolumeTest(hostedCluster, clusterOpts),
			},
			{
				name: "TestNodePoolAutoRepair",
				test: NewNodePoolAutoRepairTest(ctx, hostedCluster, hostedClusterClient, clusterOpts),
			},
			{
				name: "TestNodepoolMachineconfigGetsRolledout",
				test: NewNodePoolMachineconfigRolloutTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts),
			},
			{
				name: "TestNTOMachineConfigGetsRolledOut",
				test: NewNTOMachineConfigRolloutTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
			},

			{
				name:            "TestNTOMachineConfigAppliedInPlace",
				test:            NewNTOMachineConfigRolloutTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
				manifestBuilder: NewNTOMachineConfigInPlaceRolloutTestManifest(hostedCluster),
			},

			{
				name: "TestNodePoolReplaceUpgrade",
				test: NewNodePoolUpgradeTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts, globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage),
			},

			{
				name:            "TestNodePoolInPlaceUpgrade",
				test:            NewNodePoolUpgradeTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts, globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage),
				manifestBuilder: NewNodePoolInPlaceUpgradeTestManifest(hostedCluster, globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage),
			},

			{
				name: "KubeVirtCacheTest",
				test: NewKubeVirtCacheTest(ctx, mgtClient, hostedCluster),
			},
		}

		for _, testCase := range nodePoolTests {
			t.Run(testCase.name, func(t *testing.T) {
				executeNodePoolTest(t, ctx, mgtClient, hostedCluster, hostedClusterClient, *defaultNodepool, testCase.test, testCase.manifestBuilder)
			})
		}
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)

}

// nodePoolScaleDownToZero function will scaleDown the nodePool created for the current tests
// when it finishes the execution. It's usually summoned via defer.
func nodePoolScaleDownToZero(ctx context.Context, client crclient.Client, nodePool hyperv1.NodePool, t *testing.T) {
	err := client.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	np := nodePool.DeepCopy()
	nodePool.Spec.Replicas = &zeroReplicas
	if err = client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Error(fmt.Errorf("failed to downscale nodePool %s: %v", nodePool.Name, err), "cannot scaledown the desired nodepool")
	}
}

// nodePoolRecreate function will recreate the NodePool if that exists during the E2E test execution.
// The situation should not happen in CI but it's useful in local testing.
func nodePoolRecreate(t *testing.T, ctx context.Context, nodePool *hyperv1.NodePool, mgmtClient crclient.Client) error {
	g := NewWithT(t)
	existantNodePool := &hyperv1.NodePool{}
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existantNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
	err = mgmtClient.Delete(ctx, existantNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existant NodePool")
	t.Logf("waiting for NodePools to be recreated")
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, err
		}
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				t.Logf("WARNING: NodePool still there, will retry")
				return false, nil
			}
			t.Logf("ERROR: Cannot create the NodePool, reason: %v", err)
			return false, nil
		}
		return true, nil
	})
	t.Logf("Nodepool Recreated")

	return nil
}

type NodePoolTest interface {
	Setup(t *testing.T)
	Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node)

	NodePoolManifestBuilder
}

type NodePoolManifestBuilder interface {
	BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error)
}

func executeNodePoolTest(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client,
	defaultNodepool hyperv1.NodePool, nodePoolTest NodePoolTest, manifestBuilder NodePoolManifestBuilder) {
	t.Parallel()

	nodePoolTest.Setup(t)
	g := NewWithT(t)

	// create nodePool manifest for the test
	if manifestBuilder == nil {
		manifestBuilder = nodePoolTest
	}
	nodePool, err := manifestBuilder.BuildNodePoolManifest(defaultNodepool)
	g.Expect(err).ToNot(HaveOccurred())

	// Using default security group is main use case for OCM.
	if nodePool.Spec.Platform.Type == hyperv1.AWSPlatform {
		nodePool.Spec.Platform.AWS.SecurityGroups = nil
	}

	// Create NodePool for current test
	err = mgmtClient.Create(ctx, nodePool)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create nodePool %s: %v", nodePool.Name, err)
		}
		err = nodePoolRecreate(t, ctx, nodePool, mgmtClient)
		g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
	}

	numNodes := *nodePool.Spec.Replicas
	t.Logf("Waiting for Nodes %d\n", numNodes)
	nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hcClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
	t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

	// run test validations
	nodePoolTest.Run(t, *nodePool, nodes)

	validateNodePoolConditions(t, ctx, mgmtClient, nodePool)
}

func validateNodePoolConditions(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool) {
	expectedConditions := conditions.ExpectedNodePoolConditions()

	if nodePool.Spec.AutoScaling != nil {
		expectedConditions[hyperv1.NodePoolAutoscalingEnabledConditionType] = corev1.ConditionTrue
	} else {
		expectedConditions[hyperv1.NodePoolAutoscalingEnabledConditionType] = corev1.ConditionFalse
	}

	if nodePool.Spec.Management.AutoRepair {
		expectedConditions[hyperv1.NodePoolAutorepairEnabledConditionType] = corev1.ConditionTrue
	} else {
		expectedConditions[hyperv1.NodePoolAutorepairEnabledConditionType] = corev1.ConditionFalse
	}

	if nodePool.Spec.Arch != "" && nodePool.Spec.Platform.Type != hyperv1.AWSPlatform {
		expectedConditions[hyperv1.NodePoolValidArchPlatform] = corev1.ConditionFalse
	}

	start := time.Now()
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool); err != nil {
			t.Logf("Failed to get nodepool: %v", err)
			return false, nil
		}

		for _, condition := range nodePool.Status.Conditions {
			expectedStatus, known := expectedConditions[condition.Type]
			if !known {
				return false, fmt.Errorf("unknown condition %s", condition.Type)
			}

			if condition.Status != expectedStatus {
				t.Logf("condition %s status [%s] doesn't match the expected status [%s]", condition.Type, condition.Status, expectedStatus)
				return false, nil
			}
			t.Logf("observed condition %s status to match expected stauts [%s]", condition.Type, expectedStatus)
		}

		return true, nil
	})
	duration := time.Since(start).Round(time.Second)

	if err != nil {
		t.Fatalf("Failed to validate NodePool conditions in %s: %v", duration, err)
	}
	t.Logf("Successfully validated all expected NodePool conditions in %s", duration)
}
