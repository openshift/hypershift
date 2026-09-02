//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ensureNodeCountMatchesNodePoolReplicas verifies that every NodePool belonging
// to the HostedCluster has the number of Ready hosted cluster Nodes specified
// by its replicas. It fails the current Ginkgo assertion if a NodePool has no
// replicas set or any of its nodes are not Ready.
func ensureNodeCountMatchesNodePoolReplicas(
	ctx context.Context,
	managementClient crclient.Client,
	hostedClusterClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
) {
	GinkgoHelper()
	Expect(hostedCluster).NotTo(BeNil(), "hosted cluster should not be nil")
	if hostedCluster == nil {
		return
	}

	nodePoolList := &hyperv1.NodePoolList{}
	if err := managementClient.List(ctx, nodePoolList, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		Expect(err).NotTo(HaveOccurred(), "failed to list NodePools in namespace %s", hostedCluster.Namespace)
		return
	}
	Expect(nodePoolList.Items).NotTo(BeEmpty())

	foundNodePool := false
	for _, nodePool := range nodePoolList.Items {
		if nodePool.Spec.ClusterName != hostedCluster.Name {
			continue
		}
		foundNodePool = true
		if nodePool.Spec.AutoScaling != nil {
			// TODO: Implement post-upgrade assertions for autoscaled NodePools.
			Skip(fmt.Sprintf("post-upgrade assertions are not implemented for autoscaled NodePool %s/%s", nodePool.Namespace, nodePool.Name))
		}

		Expect(nodePool.Spec.Replicas).NotTo(BeNil(), "NodePool %s/%s replicas are not set", nodePool.Namespace, nodePool.Name)
		if nodePool.Spec.Replicas == nil {
			continue
		}

		nodeList := &corev1.NodeList{}
		Expect(hostedClusterClient.List(ctx, nodeList, crclient.MatchingLabels{
			hyperv1.NodePoolLabel: nodePool.Name,
		})).To(Succeed(), "failed to list Nodes for NodePool %s/%s", nodePool.Namespace, nodePool.Name)

		Expect(nodeList.Items).To(HaveLen(int(*nodePool.Spec.Replicas)),
			"expected %d Nodes for NodePool %s/%s, got %d",
			*nodePool.Spec.Replicas, nodePool.Namespace, nodePool.Name, len(nodeList.Items))

		for _, node := range nodeList.Items {
			ready := false
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			Expect(ready).To(BeTrue(), "Node %s for NodePool %s/%s is not Ready", node.Name, nodePool.Namespace, nodePool.Name)
		}
	}
	Expect(foundNodePool).To(BeTrue(), "expected at least one NodePool for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)
}

// ensureMachineDeploymentGeneration verifies that every MachineDeployment in
// the hosted control-plane namespace has the expected generation.
func ensureMachineDeploymentGeneration(
	ctx context.Context,
	managementClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
	expectedGeneration int64,
) {
	GinkgoHelper()
	Expect(hostedCluster).NotTo(BeNil(), "hosted cluster should not be nil")
	if hostedCluster == nil {
		return
	}

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	machineDeploymentList := &capiv1.MachineDeploymentList{}
	Expect(managementClient.List(ctx, machineDeploymentList, crclient.InNamespace(namespace))).To(Succeed(),
		"failed to list MachineDeployments in namespace %s", namespace)
	Expect(machineDeploymentList.Items).NotTo(BeEmpty(),
		"expected at least one MachineDeployment in namespace %s", namespace)

	for i := range machineDeploymentList.Items {
		machineDeployment := &machineDeploymentList.Items[i]
		Expect(machineDeployment.Generation).To(Equal(expectedGeneration),
			"MachineDeployment %s does not have expected generation %d, got %d",
			crclient.ObjectKeyFromObject(machineDeployment), expectedGeneration, machineDeployment.Generation)
	}
}

// ControlPlaneUpgradeTest upgrades the hosted cluster from N-1 to the latest release image.
func ControlPlaneUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade the control plane from N-1 to latest", func() {
		testCtx := getTestCtx()
		testCtx.SkipIfVersionBelow(e2eutil.Version422)

		ctx := testCtx.Context
		By("Fetching the HostedCluster before upgrade")
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())

		By("Preparing the latest release image for upgrade")
		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		Expect(latestImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")

		var startingVersion string
		if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
			startingVersion = hc.Status.Version.History[0].Version
		}

		By(fmt.Sprintf("Requesting control plane upgrade from version %s to image %s", startingVersion, latestImage))
		err = e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = latestImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestImage
		})
		Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

		By(fmt.Sprintf("Waiting for the control plane and data plane to reach image %s", latestImage))
		ExpectHostedClusterUpgradeToComplete(ctx, testCtx.MgmtClient, hc, latestImage)

		By("Re-fetching HostedCluster after upgrade")
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		By("Creating a hosted cluster client after upgrade")
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		By("Validating node count after upgrade")
		ensureNodeCountMatchesNodePoolReplicas(ctx, testCtx.MgmtClient, hcClient, hc)
		By("Validating MachineDeployment generation after upgrade")
		ensureMachineDeploymentGeneration(ctx, testCtx.MgmtClient, hc, 1)
	})
}

// RegisterControlPlaneUpgradeTests registers all control plane upgrade tests.
func RegisterControlPlaneUpgradeTests(getTestCtx internal.TestContextGetter) {
	ControlPlaneUpgradeTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneUpgrade] Control Plane Upgrade", Label("lifecycle", "control-plane-upgrade"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterControlPlaneUpgradeTests(func() *internal.TestContext { return testCtx })
})
