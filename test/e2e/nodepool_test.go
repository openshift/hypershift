//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
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
						name: "TestNodePoolDay2Tags",
						test: NewNodePoolDay2TagsTest(ctx, mgtClient, hostedCluster, clusterOpts),
					},
					{
						name: "TestSpotTerminationHandler",
						test: NewSpotTerminationHandlerTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts),
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
						name: "OpenStackAdvancedTest",
						test: NewOpenStackAdvancedTest(ctx, mgtClient, hostedCluster),
					},
					{
						name: "TestNTOPerformanceProfile",
						test: NewNTOPerformanceProfileTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
					},
					{
						name: "TestNodePoolPrevReleaseN1",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.N1MinorReleaseImage, clusterOpts, true),
					},
					{
						name: "TestNodePoolPrevReleaseN2",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.N2MinorReleaseImage, clusterOpts, true),
					},
					{
						name: "TestNodePoolPrevReleaseN3",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.N3MinorReleaseImage, clusterOpts, true),
					},
					{
						name: "TestNodePoolPrevReleaseN4",
						test: NewNodePoolPrevReleaseCreateTest(hostedCluster, globalOpts.N4MinorReleaseImage, clusterOpts, false),
					},
					{
						name: "TestMirrorConfigs",
						test: NewMirrorConfigsTest(ctx, mgtClient, hostedCluster, hostedClusterClient),
					},
					{
						name: "TestImageTypes",
						test: NewNodePoolImageTypeTest(ctx, mgtClient, hostedCluster, hostedClusterClient, clusterOpts),
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
					test: NewAdditionalTrustBundlePropagation(ctx, mgtClient, hostedCluster, hostedClusterClient),
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
				if !globalOpts.ConfigurableClusterOptions.AWSMultiArch && !globalOpts.ConfigurableClusterOptions.AzureMultiArch {
					t.Skip("test only supported on multi-arch clusters")
				}
				if globalOpts.Platform != hyperv1.AWSPlatform && globalOpts.Platform != hyperv1.AzurePlatform {
					t.Skip("test only supported on platform AWS and Azure")
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
			// TestSpotTerminationHandler requires new sqs permissions for the NodePool role,
			// so we want to test the real roles.
			if i == 0 {
				clusterOpts.AWSPlatform.SharedRole = false
			}

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
			}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "node-pool", globalOpts.ServiceAccountSigningKey)
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

type SupportedVersionSkewChecker interface {
	ExpectedSupportedVersionSkew() bool
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
	g.Expect(nodePoolTest.SetupInfra(t)).To(Succeed(), "should succeed setting up the infra after creating the nodepool")
	defer func() {
		g.Expect(nodePoolTest.TeardownInfra(t)).To(Succeed(), "should succeed cleaning up infra customizations")
	}()

	// Determine if this version is expected to be supported
	expectedSupportedVersionSkew := true
	if checker, ok := nodePoolTest.(SupportedVersionSkewChecker); ok {
		expectedSupportedVersionSkew = checker.ExpectedSupportedVersionSkew()
	}

	// For unsupported versions, only validate the condition is set correctly
	// Skip node readiness checks as nodes may not become ready for incompatible versions
	if !expectedSupportedVersionSkew {
		t.Logf("NodePool version is outside supported skew, validating condition only (skipping node readiness check)")
		validateNodePoolConditions(t, ctx, mgmtClient, nodePool, expectedSupportedVersionSkew)
		return
	}

	// Validate that CAPI v1 condition messages are bubbled up during machine provisioning.
	// This checks that the AllMachinesReady condition is populated with CAPI-derived
	// machine-level details before machines are fully ready.
	if nodePool.Spec.Replicas != nil && *nodePool.Spec.Replicas > 0 {
		validateCAPIConditionBubblingDuringProvisioning(t, ctx, mgmtClient, nodePool)
	}

	// For supported versions, run full validation including node readiness
	nodes := e2eutil.WaitForReadyNodesByNodePool(t, ctx, hcClient, nodePool, hostedCluster.Spec.Platform.Type)
	// We want to make sure all conditions are met and in a deterministic known state before running the tests to avoid false positives.
	// https://issues.redhat.com/browse/OCPBUGS-52983.
	validateNodePoolConditions(t, ctx, mgmtClient, nodePool, expectedSupportedVersionSkew)

	// TestNTOPerformanceProfile fails on 4.16 and older if we don't wait for the rollout here with
	// ValidationFailed(ConfigMap "pp-test" not found)
	// This root cause of this failure is unknown but doesn't seem worth the time to figure out since
	// the NTO performance profile code was heavily refactored in 4.17.
	if e2eutil.IsLessThan(e2eutil.Version417) {
		// Wait for the rollout to be complete
		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster)
	}

	// run test validations
	nodePoolTest.Run(t, *nodePool, nodes)

	validateNodePoolConditions(t, ctx, mgmtClient, nodePool, expectedSupportedVersionSkew)
}

func validateNodePoolConditions(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, expectedSupportedVersionSkew bool) {
	var expectedConditions map[string]corev1.ConditionStatus

	if !expectedSupportedVersionSkew {
		// For unsupported versions, only validate the SupportedVersionSkew condition
		expectedConditions = map[string]corev1.ConditionStatus{
			hyperv1.NodePoolSupportedVersionSkewConditionType: corev1.ConditionFalse,
		}
	} else {
		// For supported versions, validate all conditions
		expectedConditions = conditions.ExpectedNodePoolConditions(nodePool)
	}

	var predicates []e2eutil.Predicate[*hyperv1.NodePool]
	for conditionType, conditionStatus := range expectedConditions {
		condition := e2eutil.Condition{
			Type:   conditionType,
			Status: metav1.ConditionStatus(conditionStatus),
		}

		// For CAPI-derived conditions in steady state, also validate Reason and Message
		// to ensure CAPI v1 condition messages are properly aggregated and bubbled up.
		// When all machines are ready and healthy, the aggregation pipeline should produce
		// Reason=AsExpected and Message="All is well".
		if expectedSupportedVersionSkew {
			switch conditionType {
			case hyperv1.NodePoolAllMachinesReadyConditionType, hyperv1.NodePoolAllNodesHealthyConditionType:
				condition.Reason = hyperv1.AsExpectedReason
				condition.Message = hyperv1.AllIsWellMessage
			}
		}

		predicates = append(predicates, e2eutil.ConditionPredicate[*hyperv1.NodePool](condition))
	}

	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to have correct status", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			return nodePool, err
		},
		predicates, e2eutil.WithoutConditionDump(), e2eutil.WithTimeout(20*time.Minute),
		e2eutil.WithInterval(15*time.Second), // Reduce polling frequency from 3s default to prevent client rate limiting
	)
}

// validateCAPIConditionBubblingDuringProvisioning validates that during machine provisioning,
// CAPI v1 Machine conditions are properly bubbled up to the NodePool.
//
// This specifically tests the nil-condition handling: when CAPI Machines exist but have not
// yet reported their Ready or NodeHealthy conditions (nil), the NodePool conditions must
// show False with machine-level details — not incorrectly True.
//
// AllNodesHealthy is the primary signal because the CAPI NodeHealthy condition stays nil
// until the node actually joins the cluster and kubelet reports health. This takes minutes
// on any cloud platform, creating a large reliable window where:
//   - Correct code: AllNodesHealthy=False with "machines are not healthy"
//   - Broken code:  AllNodesHealthy=True (nil conditions silently ignored)
//
// AllMachinesReady is checked as well but the nil window is shorter (CAPI provider sets
// Ready=False quickly after instance launch), so it uses a softer assertion.
func validateCAPIConditionBubblingDuringProvisioning(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool) {
	t.Helper()
	t.Log("Validating CAPI v1 condition message bubbling during machine provisioning")

	// AllNodesHealthy: hard assertion.
	// NodeHealthy stays nil until the node joins the cluster (minutes). During this entire
	// window, AllNodesHealthy MUST be False with the aggregated message format.
	// If it shows True, nil conditions are being silently ignored.
	nodesUnhealthyObserved := pollForConditionFalseWithAggregatedMessage(t, ctx, client, nodePool,
		hyperv1.NodePoolAllNodesHealthyConditionType, "healthy")
	if !nodesUnhealthyObserved {
		t.Errorf("AllNodesHealthy was never observed as False with aggregated 'machines are not healthy' message "+
			"during provisioning. This indicates CAPI Machine nil NodeHealthy conditions are being silently "+
			"ignored instead of treated as unhealthy. NodePool: %s/%s", nodePool.Namespace, nodePool.Name)
	}

	// AllMachinesReady: soft assertion (log-only).
	// The nil window for Ready condition is brief because the CAPI provider sets Ready=False
	// quickly after instance launch. We may miss it.
	machinesUnreadyObserved := pollForConditionFalseWithAggregatedMessage(t, ctx, client, nodePool,
		hyperv1.NodePoolAllMachinesReadyConditionType, "ready")
	if !machinesUnreadyObserved {
		t.Logf("AllMachinesReady was not observed as False with aggregated message during provisioning " +
			"(CAPI provider may have set Ready condition before we could observe nil state)")
	}
}

// pollForConditionFalseWithAggregatedMessage polls the NodePool until it observes the given
// condition as False with a message containing "machines are not <state>" (the format produced
// by aggregateMachineReasonsAndMessages). Polling stops when either:
//   - The condition is False with the expected message format → returns true
//   - The condition transitions to True (machines became ready) → returns false
//   - The timeout (5 min) is reached → returns false
func pollForConditionFalseWithAggregatedMessage(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, conditionType, state string) bool {
	t.Helper()

	observed := false
	expectedSubstring := "machines are not " + state

	_ = wait.PollUntilContextTimeout(ctx, 3*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		np := &hyperv1.NodePool{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), np); err != nil {
			return false, nil
		}

		for _, cond := range np.Status.Conditions {
			if cond.Type != conditionType {
				continue
			}

			if cond.Status == corev1.ConditionFalse && strings.Contains(cond.Message, expectedSubstring) {
				t.Logf("CAPI bubbling confirmed for %s: Status=False, Reason=%s", conditionType, cond.Reason)
				observed = true
				return true, nil // success — stop polling
			}

			if cond.Status == corev1.ConditionTrue {
				// Condition is True — machines became ready before we saw the aggregated False state
				return true, nil // stop polling
			}

			// False but without the expected message (e.g. "No Machines are created" before
			// CAPI machines exist) — keep polling until machines are created
			return false, nil
		}

		// Condition not yet set on NodePool — keep polling
		return false, nil
	})

	return observed
}
