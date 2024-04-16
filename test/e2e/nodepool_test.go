//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
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

type HostedClusterNodePoolTestCases struct {
	build HostedClusterNodePoolTestCasesBuilderFn
	setup func(t *testing.T)
}

type HostedClusterNodePoolTestCasesBuilderFn func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) []NodePoolTestCase

type NodePoolTestCase struct {
	name            string
	test            NodePoolTest
	manifestBuilder NodePoolManifestBuilder
	infraSetup      func(t *testing.T) error
}

func TestNodePool(t *testing.T) {
	t.Parallel()

	nodePoolTestCasesPerHostedCluster := []HostedClusterNodePoolTestCases{
		{
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) []NodePoolTestCase {
				return []NodePoolTestCase{
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
						test: NewNTOMachineConfigRolloutTest(ctx, mgtClient, hostedCluster, hostedClusterClient, false),
					},

					{
						name:            "TestNTOMachineConfigAppliedInPlace",
						test:            NewNTOMachineConfigRolloutTest(ctx, mgtClient, hostedCluster, hostedClusterClient, true),
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
					{
						name: "TestRollingUpgrade",
						test: NewRollingUpgradeTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeVirtQoSClassGuaranteedTest",
						test: NewKubeVirtQoSClassGuaranteedTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeKubeVirtJsonPatchTest",
						test: NewKubeKubeVirtJsonPatchTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeVirtNodeSelectorTest",
						test: NewKubeKubeVirtNodeSelectorTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeVirtNodeMultinetTest",
						test: NewKubeVirtMultinetTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "TestNTOPerformanceProfile",
						test: NewNTOPerformanceProfileTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
					},
				}
			},
		},
		// This kubevirt test need to run at different hosted cluster
		{
			setup: func(t *testing.T) {
				if globalOpts.Platform != hyperv1.KubevirtPlatform {
					t.Skip("tests only supported on platform KubeVirt")
				}
			},
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) []NodePoolTestCase {
				return []NodePoolTestCase{{
					name: "KubeVirtNodeAdvancedMultinetTest",
					test: NewKubeVirtAdvancedMultinetTest(ctx, mgtClient, hostedCluster),
				}}
			},
		},
	}

	executeNodePoolTests(t, nodePoolTestCasesPerHostedCluster)
}

func executeNodePoolTests(t *testing.T, nodePoolTestCasesPerHostedCluster []HostedClusterNodePoolTestCases) {
	for i, _ := range nodePoolTestCasesPerHostedCluster {
		t.Run(fmt.Sprintf("HostedCluster%d", i), func(t *testing.T) {
			nodePoolTestCases := nodePoolTestCasesPerHostedCluster[i]
			if nodePoolTestCases.setup != nil {
				nodePoolTestCases.setup(t)
			}
			t.Parallel()
			clusterOpts := globalOpts.DefaultClusterOptions(t)
			// We set replicas to 0 in order to allow the inner tests to
			// create their own NodePools with the proper replicas
			clusterOpts.NodePoolReplicas = 0

			ctx, cancel := context.WithCancel(testContext)
			defer cancel()
			e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
				hostedClusterClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

				// Get the newly created default NodePool
				nodepools := &hyperv1.NodePoolList{}
				if err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
					t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
				}
				g.Expect(nodepools.Items).ToNot(BeEmpty())
				defaultNodepool := &nodepools.Items[0]
				testCases := nodePoolTestCases.build(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts)
				for _, testCase := range testCases {
					t.Run(testCase.name, func(t *testing.T) {
						executeNodePoolTest(t, ctx, mgtClient, hostedCluster, hostedClusterClient, *defaultNodepool, testCase.test, testCase.manifestBuilder)
					})
				}
			}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
		})
	}
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
	existingNodePool := &hyperv1.NodePool{}
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existingNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed getting existent nodepool")
	err = mgmtClient.Delete(ctx, existingNodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existent NodePool")
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
	InfraSetup
}

type NodePoolManifestBuilder interface {
	BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error)
}

type InfraSetup interface {
	SetupInfra(t *testing.T) error
	TeardownInfra(t *testing.T) error
}

type DummyInfraSetup struct{}

func (i *DummyInfraSetup) SetupInfra(*testing.T) error {
	return nil
}
func (i *DummyInfraSetup) TeardownInfra(*testing.T) error {
	return nil
}

func executeNodePoolTest(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client,
	defaultNodepool hyperv1.NodePool, nodePoolTest NodePoolTest, manifestBuilder NodePoolManifestBuilder) {
	t.Parallel()

	nodePoolTest.Setup(t)
	g := NewWithT(t)

	// create nodePool manifest for the test
	if manifestBuilder == nil {
		g.Expect(nodePoolTest).ToNot(BeNil())
		manifestBuilder = nodePoolTest
	}
	nodePool, err := manifestBuilder.BuildNodePoolManifest(defaultNodepool)
	g.Expect(err).ToNot(HaveOccurred(), "should success preparing nodepool")

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

	// Extra setup at some test after nodepool creation
	g.Expect(nodePoolTest.SetupInfra(t)).To(Succeed(), "should succeed seting up the infra after creating the nodepool")
	defer func() {
		g.Expect(nodePoolTest.TeardownInfra(t)).To(Succeed(), "should succeed cleaning up infra customizations")
	}()

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

	t.Logf("validating status for nodepool %s/%s", nodePool.Namespace, nodePool.Name)
	start := time.Now()
	previousResourceVersion := ""
	previousConditions := map[string]hyperv1.NodePoolCondition{}
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool); err != nil {
			t.Logf("Failed to get nodepool: %v", err)
			return false, nil
		}

		if nodePool.ResourceVersion == previousResourceVersion {
			// nothing's changed since the last time we checked
			return false, nil
		}
		previousResourceVersion = nodePool.ResourceVersion

		currentConditions := map[string]hyperv1.NodePoolCondition{}
		conditionsValid := true
		for i, condition := range nodePool.Status.Conditions {
			expectedStatus, known := expectedConditions[condition.Type]
			if !known {
				return false, fmt.Errorf("unknown condition %s", condition.Type)
			}
			conditionsValid = conditionsValid && (condition.Status == expectedStatus)

			currentConditions[condition.Type] = nodePool.Status.Conditions[i]
			if conditionsIdentical(currentConditions[condition.Type], previousConditions[condition.Type]) {
				// no need to spam anything, we already said it when we processed this last time
				continue
			}
			prefix := ""
			if condition.Status != expectedStatus {
				prefix = "in"
			}
			msg := fmt.Sprintf("%scorrect condition: wanted %s=%s, got %s=%s", prefix, condition.Type, expectedStatus, condition.Type, condition.Status)
			if condition.Reason != "" {
				msg += ": " + condition.Reason
			}
			if condition.Message != "" {
				msg += "(" + condition.Message + ")"
			}
			t.Log(msg)
		}
		previousConditions = currentConditions

		return conditionsValid, nil
	})
	duration := time.Since(start).Round(time.Second)

	if err != nil {
		t.Fatalf("Failed to validate NodePool conditions in %s: %v", duration, err)
	}
	t.Logf("Successfully validated all expected NodePool conditions in %s", duration)
}

func conditionsIdentical(a, b hyperv1.NodePoolCondition) bool {
	return a.Type == b.Type && a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}
