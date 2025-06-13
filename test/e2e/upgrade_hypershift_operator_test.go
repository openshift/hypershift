//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/cluster-api/api/v1beta1"
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
	var machineDeploymentMap map[string]*v1beta1.MachineDeployment

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
			// TODO: remove when the switch to CPOv2 is merged and the new HO version is used as the init image for this test.
			obj.Annotations["hypershift.openshift.io/cpo-v2"] = "true"
		}
	}

	operatorImage, err := e2eutil.GetHyperShiftOperatorImage(ctx, client, globalOpts.HOInstallationOptions)
	g.Expect(err).ToNot(gomega.HaveOccurred(), "Getting HyperShiftOperator image shouldn't return errors")
	t.Logf("Observed pre-upgrade HyperShift Operator image %q", operatorImage)

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

			// Get the MachineDeployments
			machineDeployments := &v1beta1.MachineDeploymentList{}
			hcpNameSpace = manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

			err = mgmtClient.List(ctx, machineDeployments, crclient.InNamespace(hcpNameSpace))

			g.Expect(err).ToNot(gomega.HaveOccurred(),
				"Listing MachineDeployments in namespace %s shouldn't return errors", hcpNameSpace)
			g.Expect(machineDeployments.Items).ToNot(gomega.BeEmpty(),
				"Should find MachineDeployments in namespace %s", hcpNameSpace)
			g.Expect(len(machineDeployments.Items)).To(gomega.BeEquivalentTo(len(nodepools.Items)),
				"Number of MachineDeployments and NodePools should match")

			machineDeploymentMap = make(map[string]*v1beta1.MachineDeployment)
			t.Logf("Found %d MachineDeployments", len(machineDeployments.Items))
			for i := range machineDeployments.Items {
				t.Logf("Found MachineDeployment %s", machineDeployments.Items[i].Name)
				machineDeploymentMap[machineDeployments.Items[i].Name] = &machineDeployments.Items[i]
			}

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
						"Pre-upgrade and post-upgrade NodePool current config should match")
					g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(
						gomega.Equal(preUpgradeNodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]),
						"Pre-upgrade and post-upgrade NodePool current config version should match")

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

				postUpgradeMachineDeployments := &v1beta1.MachineDeploymentList{}
				err = mgmtClient.List(ctx, postUpgradeMachineDeployments, crclient.InNamespace(hcpNameSpace))
				if err != nil {
					gomega.StopTrying(fmt.Sprintf("Error listing MachineDeployments: %v", err)).Now()
				}

				if len(postUpgradeMachineDeployments.Items) != len(machineDeploymentMap) {
					gomega.StopTrying(fmt.Sprintf("Number of MachineDeployments changed from %d to %d", len(machineDeploymentMap), len(postUpgradeMachineDeployments.Items))).Now()
				}
				for _, machineDeployment := range postUpgradeMachineDeployments.Items {
					t.Logf("Verifying MachineDeployment %s", machineDeployment.Name)
					var preUpgradeMachineDeployment *v1beta1.MachineDeployment
					var ok bool
					if preUpgradeMachineDeployment, ok = machineDeploymentMap[machineDeployment.Name]; !ok {
						gomega.StopTrying(fmt.Sprintf("MachineDeployment %s not found", machineDeployment.Name)).Now()
					}

					t.Logf("Generation: Got %d", machineDeployment.Generation)

					// Check if the machine deployment has been updated
					g.Expect(machineDeployment.Generation).To(gomega.Equal(preUpgradeMachineDeployment.Generation),
						"Pre-upgrade and post-upgrade MachineDeployment generations should match")
				}
				return true
			}, "5m", "1s").Should(gomega.BeTrue(), "Verification should consistently succeed for 5 minutes")
		})).To(gomega.BeTrue(), "Verify upgrade invariants should succeed")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ho-upgrade", globalOpts.ServiceAccountSigningKey)
}
