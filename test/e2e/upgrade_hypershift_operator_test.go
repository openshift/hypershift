//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/supportedversion"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nodePoolAnnotationCurrentConfig        = "hypershift.openshift.io/nodePoolCurrentConfig"
	nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	hostedClusterUpgradeTestLabel          = "hypershift.openshift.io/upgrade-test"
)

// TestUpgradeHyperShiftOperator validates that a HyperShift Operator upgrade won't unnecessarily cause a node rollout.
// The test must be triggered via the flag upgrade.run-tests
func TestUpgradeHyperShiftOperator(t *testing.T) {
	if !globalOpts.RunUpgradeTest {
		// This test should be triggered intentionally by setting the option above,
		// otherwise skip it.
		t.SkipNow()
	}
	var err error

	var mgmtClient crclient.Client
	var hostedCluster *hyperv1.HostedCluster
	var hcpNameSpace string
	var nodePoolsMap map[string]*hyperv1.NodePool
	var machineDeploymentMap map[string]int64

	hyperShiftOperatorLatestImage := globalOpts.HyperShiftOperatorLatestImage

	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)

	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	t.Log("Starting HyperShift Operator upgrade test")
	g := gomega.NewWithT(t)
	client, err := e2eutil.GetClient()
	g.Expect(err).ToNot(gomega.HaveOccurred(), "Getting kubernetes client shouldn't return errors")

	zones := strings.Split(globalOpts.ConfigurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		t.Log("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}

	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch obj := o.(type) {
		case *hyperv1.HostedCluster:

			// Add a label to identify the HostedCluster for upgrade tests in case they're leaked
			// and need to be cleaned up.
			if obj.Labels == nil {
				obj.Labels = make(map[string]string)
			}
			obj.Labels[hostedClusterUpgradeTestLabel] = "true"
		}
	}

	operatorImage, err := e2eutil.GetHyperShiftOperatorImage(ctx, client, globalOpts.HOInstallationOptions)
	g.Expect(err).ToNot(gomega.HaveOccurred(), "Getting HyperShiftOperator image shouldn't return errors")
	t.Logf("Observed pre-upgrade HyperShift Operator image %q", operatorImage)
	useCAPIv1Beta1 := e2eutil.IsLessThan(e2eutil.Version423)

	// Shared role credential reconciliation only landed on 4.21+.
	// Check the pre-upgrade HO's advertised version, not the release image version.
	// The supported-versions ConfigMap is reconciled asynchronously by the HO after
	// its deployment becomes Available, so poll until the overall test times out.
	var preUpgradeHOVersion semver.Version
	err = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		v, err := supportedversion.GetLatestSupportedOCPVersion(ctx, client)
		if err != nil {
			t.Logf("Waiting for supported-versions ConfigMap: %v", err)
			return false, nil
		}
		preUpgradeHOVersion = v
		return true, nil
	})
	g.Expect(err).ToNot(gomega.HaveOccurred(), "reading pre-upgrade HO version from supported-versions ConfigMap")
	t.Logf("Pre-upgrade HO latest supported version: %s", preUpgradeHOVersion)
	if preUpgradeHOVersion.LT(e2eutil.Version421) {
		t.Log("Pre-upgrade HO < 4.21, disabling shared role")
		clusterOpts.AWSPlatform.SharedRole = false
	}

	t.Log("Executing upgrade test")
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g gomega.Gomega, mc crclient.Client, hc *hyperv1.HostedCluster) {
		t.Logf("HostedCluster %s created", crclient.ObjectKeyFromObject(hc))
		t.Log("Waiting for HostedCluster client to be ready")
		_ = e2eutil.WaitForGuestClient(t, ctx, mc, hc)

		t.Log("Nodes are ready")
		mgmtClient = mc
		hostedCluster = hc

		g.Expect(t.Run("Calculate HyperShift Operator upgrade invariants", func(t *testing.T) {
			t.Log("Calculating HyperShift Operator upgrade invariants")
			g := gomega.NewWithT(t)

			// Get the newly created default NodePool
			nodepools := &hyperv1.NodePoolList{}
			err = mgmtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace))

			g.Expect(err).ToNot(gomega.HaveOccurred(), "Listing nodepools in namespace %s shouldn't return errors",
				hostedCluster.Namespace)
			g.Expect(nodepools.Items).ToNot(gomega.BeEmpty(), "Should find NodePools in namespace %s",
				hostedCluster.Namespace)

			nodePoolsMap = make(map[string]*hyperv1.NodePool, len(nodepools.Items))
			t.Logf("Found %d NodePools", len(nodepools.Items))
			for i := range nodepools.Items {
				t.Logf("Found NodePool %s", nodepools.Items[i].Name)
				nodePoolsMap[nodepools.Items[i].Name] = &nodepools.Items[i]
			}

			hcpNameSpace = manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			machineDeploymentMap, err = listMachineDeploymentGenerations(ctx, mgmtClient, hcpNameSpace, useCAPIv1Beta1)
			g.Expect(err).ToNot(gomega.HaveOccurred())
			g.Expect(len(machineDeploymentMap)).To(gomega.BeEquivalentTo(len(nodepools.Items)),
				"Number of MachineDeployments and NodePools should match")
			t.Logf("Found %d MachineDeployments", len(machineDeploymentMap))
		})).To(gomega.BeTrue(), "Calculating HyperShift Operator upgrade invariants should succeed")

		g.Expect(t.Run("Upgrade HyperShift Operator", func(t *testing.T) {
			t.Logf("Upgrading HyperShift Operator to image %s", hyperShiftOperatorLatestImage)
			installOptions := globalOpts.HOInstallationOptions
			installOptions.HyperShiftOperatorLatestImage = hyperShiftOperatorLatestImage
			// Note that we're replacing previous HO installation cleanup function with the new one
			err = e2eutil.InstallHyperShiftOperator(ctx, installOptions)
			if err != nil {
				t.Fatal("Failed to upgrade HyperShift Operator")
			}
		})).To(gomega.BeTrue(), "Upgrade HyperShift Operator should succeed")

		operatorImage, err := e2eutil.GetHyperShiftOperatorImage(ctx, mc, globalOpts.HOInstallationOptions)
		g.Expect(err).ToNot(gomega.HaveOccurred(), "Getting HyperShiftOperator image shouldn't return errors")
		g.Expect(operatorImage).To(gomega.Equal(hyperShiftOperatorLatestImage))

		t.Logf("Observed post-upgrade HyperShift Operator image %q", operatorImage)

		g.Expect(t.Run("Verify upgrade invariants", func(t *testing.T) {
			t.Log("Verifying upgrade invariants")
			namespace := hostedCluster.Namespace
			g := gomega.NewWithT(t)

			g.Consistently(func(g gomega.Gomega) bool {
				postUpgradeNodePools := &hyperv1.NodePoolList{}
				err = mgmtClient.List(ctx, postUpgradeNodePools, crclient.InNamespace(namespace))
				if err != nil {
					t.Logf("error in listing nodepools in namespace %s", namespace)
					// Try again since it might be some intermittent error
					return true
				}
				if len(postUpgradeNodePools.Items) != len(nodePoolsMap) {
					gomega.StopTrying(fmt.Sprintf("Number of NodePools changed from %d to %d", len(nodePoolsMap), len(postUpgradeNodePools.Items))).Now()
				}

				for _, nodePool := range postUpgradeNodePools.Items {
					t.Logf("Verifying NodePool %s", nodePool.Name)
					var preUpgradeNodePool *hyperv1.NodePool
					var ok bool

					if preUpgradeNodePool, ok = nodePoolsMap[nodePool.Name]; !ok {
						gomega.StopTrying(fmt.Sprintf("NodePool %s not found", nodePool.Name)).Now()
					}

					t.Logf("Generation: %d", nodePool.Generation)
					t.Logf("CurrentConfig: %s", nodePool.Annotations[nodePoolAnnotationCurrentConfig])
					t.Logf("CurrentConfigVersion:%s", nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion])

					// Check if the node pool has been updated
					g.Expect(nodePool.Generation).To(gomega.Equal(preUpgradeNodePool.Generation),
						"Pre-upgrade and post-upgrade NodePool generations should match")
					g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfig]).To(
						gomega.Equal(preUpgradeNodePool.Annotations[nodePoolAnnotationCurrentConfig]),
						"Pre-upgrade and post-upgrade NodePool current config should match",
					)
					g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(
						gomega.Equal(preUpgradeNodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]),
						"Pre-upgrade and post-upgrade NodePool current config version should match",
					)

					conditions, err := e2eutil.Conditions(&nodePool)
					if err != nil {
						gomega.StopTrying(fmt.Sprintf("Error getting NodePool conditions: %v", err)).Now()
					}
					targetConditions := sets.NewString(hyperv1.NodePoolUpdatingVersionConditionType,
						hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType)
					for _, c := range conditions {
						if targetConditions.Has(c.Type) {
							t.Logf("Found condition %s of NodePool %s", c.String(), nodePool.Name)
							g.Expect(c.Status).To(gomega.Equal(metav1.ConditionFalse), "Condition %s of nodepool %s shouldn't be True", nodePool.Name, c.Type)
						}
					}
				}

				postUpgradeMachineDeployments, err := listMachineDeploymentGenerations(ctx, mgmtClient, hcpNameSpace, useCAPIv1Beta1)
				if err != nil {
					gomega.StopTrying(fmt.Sprintf("Error listing MachineDeployments: %v", err)).Now()
				}

				if len(postUpgradeMachineDeployments) != len(machineDeploymentMap) {
					gomega.StopTrying(fmt.Sprintf("Number of MachineDeployments changed from %d to %d", len(machineDeploymentMap), len(postUpgradeMachineDeployments))).Now()
				}
				for name, generation := range postUpgradeMachineDeployments {
					t.Logf("Verifying MachineDeployment %s", name)
					var ok bool
					var preUpgradeGeneration int64
					if preUpgradeGeneration, ok = machineDeploymentMap[name]; !ok {
						gomega.StopTrying(fmt.Sprintf("MachineDeployment %s not found", name)).Now()
					}

					t.Logf("Generation: Got %d", generation)

					// Check if the machine deployment has been updated
					g.Expect(generation).To(gomega.Equal(preUpgradeGeneration),
						"Pre-upgrade and post-upgrade MachineDeployment generations should match")
				}
				return true
			}, "5m", "1s").Should(gomega.BeTrue(), "Verification should consistently succeed for 5 minutes")
		})).To(gomega.BeTrue(), "Verify upgrade invariants should succeed")
	}).WithHOUpgrade().Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ho-upgrade", globalOpts.ServiceAccountSigningKey)
}

func listMachineDeploymentGenerations(ctx context.Context, client crclient.Client, namespace string, useV1Beta1 bool) (map[string]int64, error) {
	generations := make(map[string]int64)
	if useV1Beta1 {
		machineDeployments := &capiv1beta1.MachineDeploymentList{}
		if err := client.List(ctx, machineDeployments, crclient.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("listing MachineDeployments at cluster.x-k8s.io/v1beta1 in namespace %s: %w", namespace, err)
		}
		for i := range machineDeployments.Items {
			generations[machineDeployments.Items[i].Name] = machineDeployments.Items[i].Generation
		}
	} else {
		machineDeployments := &capiv1beta2.MachineDeploymentList{}
		if err := client.List(ctx, machineDeployments, crclient.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("listing MachineDeployments at cluster.x-k8s.io/v1beta2 in namespace %s: %w", namespace, err)
		}
		for i := range machineDeployments.Items {
			generations[machineDeployments.Items[i].Name] = machineDeployments.Items[i].Generation
		}
	}
	if len(generations) == 0 {
		return nil, fmt.Errorf("no MachineDeployments found in namespace %s", namespace)
	}
	return generations, nil
}
