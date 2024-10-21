//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/conditions"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type HostedClusterNodePoolTestCasesBuilderFn func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase

type NodePoolTestCase struct {
	name            string
	test            NodePoolTest
	manifestBuilder NodePoolManifestBuilder
}

func TestNodePool(t *testing.T) {
	t.Parallel()

	nodePoolTestCasesPerHostedCluster := []HostedClusterNodePoolTestCases{
		{
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase {
				return []NodePoolTestCase{
					{
						name: "TestKMSRootVolumeEncryption",
						test: NewKMSRootVolumeTest(ctx, hostedCluster, clusterOpts),
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
						name: "KubeVirtJsonPatchTest",
						test: NewKubeVirtJsonPatchTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeVirtNodeSelectorTest",
						test: NewKubeVirtNodeSelectorTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "KubeVirtNodeMultinetTest",
						test: NewKubeVirtMultinetTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "TestNTOPerformanceProfile",
						test: NewNTOPerformanceProfileTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
					},
					{
						name: "TestNodePoolPrevReleaseN1",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.n1MinorReleaseImage, clusterOpts),
					},
					{
						name: "TestNodePoolPrevReleaseN2",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.n2MinorReleaseImage, clusterOpts),
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
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase {
				return []NodePoolTestCase{{
					name: "KubeVirtNodeAdvancedMultinetTest",
					test: NewKubeVirtAdvancedMultinetTest(ctx, mgtClient, hostedCluster),
				}}
			},
		},
		{
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase {
				return []NodePoolTestCase{{
					name: "TestAdditionalTrustBundlePropagation",
					test: NewAdditionalTrustBundlePropagation(ctx, mgtClient, hostedCluster),
				}}
			},
		},
	}

	executeNodePoolTests(t, nodePoolTestCasesPerHostedCluster)
}

func TestNodePoolMultiArch(t *testing.T) {
	t.Parallel()
	nodePoolTestCasesPerHostedCluster := []HostedClusterNodePoolTestCases{
		{
			setup: func(t *testing.T) {
				if !globalOpts.configurableClusterOptions.AWSMultiArch {
					t.Skip("test only supported on multi-arch clusters")
				}
				if globalOpts.Platform != hyperv1.AWSPlatform {
					t.Skip("test only supported on platform AWS")
				}
				t.Log("Starting NodePoolArm64CreateTest.")
			},
			build: func(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) []NodePoolTestCase {
				return []NodePoolTestCase{
					{
						name: "TestNodePoolArm64Create",
						test: NewNodePoolArm64CreateTest(hostedCluster),
					},
				}
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

			// On OpenStack, we need to create at least one replica of the default nodepool
			// so we can create the Route53 record for the ingress router. If we don't do that,
			// the HostedCluster conditions won't be met and the test will fail as some operators
			// will be marked as degraded.
			if globalOpts.Platform == hyperv1.OpenStackPlatform {
				clusterOpts.NodePoolReplicas = 1
			}

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
			}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey, globalOpts.DisableTearDown)
		})
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

	nodes := e2eutil.WaitForReadyNodesByNodePool(t, ctx, hcClient, nodePool, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

	// run test validations
	nodePoolTest.Run(t, *nodePool, nodes)

	validateNodePoolConditions(t, ctx, mgmtClient, nodePool)
}

func validateNodePoolConditions(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool) {
	expectedConditions := conditions.ExpectedNodePoolConditions(nodePool)
	var predicates []e2eutil.Predicate[*hyperv1.NodePool]
	for conditionType, conditionStatus := range expectedConditions {
		predicates = append(predicates, e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
			Type:   conditionType,
			Status: metav1.ConditionStatus(conditionStatus),
		}))
	}

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to have correct status", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			return nodePool, err
		},
		predicates, e2eutil.WithoutConditionDump(),
	)
}
