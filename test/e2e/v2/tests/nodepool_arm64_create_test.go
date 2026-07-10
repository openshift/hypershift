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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterNodePoolArm64Tests registers ARM64 NodePool test cases.
func RegisterNodePoolArm64Tests(getTestCtx internal.TestContextGetter) {
	NodePoolArm64CreateTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolArm64] NodePool ARM64", Label("nodepool-arm64"), func() {
	var testCtx *internal.TestContext
	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})
	RegisterNodePoolArm64Tests(func() *internal.TestContext { return testCtx })
})

// NodePoolArm64CreateTest creates an ARM64 NodePool, waits for the node to be ready,
// and validates that an actual ARM64 node comes up with the correct architecture label.
func NodePoolArm64CreateTest(getTestCtx internal.TestContextGetter) {
	It("When creating an ARM64 NodePool, it should provision a node with ARM64 architecture", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()
		hc := testCtx.GetHostedCluster()
		if hc.Spec.Platform.Type != hyperv1.AWSPlatform && hc.Spec.Platform.Type != hyperv1.AzurePlatform {
			Skip("ARM64 NodePool test only supported on AWS and Azure platforms")
		}
		// Check if the HostedCluster supports multi-arch
		if hc.Status.PayloadArch != hyperv1.Multi {
			Skip("ARM64 NodePool test requires a multi-arch release image")
		}
		ctx := testCtx.Context
		hcClient := testCtx.GetHostedClusterClient()
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "arm64", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Arch = hyperv1.ArchitectureARM64
			// Use the same release image as the HostedCluster
			pool.Spec.Release.Image = hc.Spec.Release.Image
			if pool.Spec.Platform.Type == hyperv1.AWSPlatform {
				pool.Spec.Platform.AWS.InstanceType = "m6g.large"
			} else if pool.Spec.Platform.Type == hyperv1.AzurePlatform {
				pool.Spec.Platform.Azure.VMSize = "Standard_D4ps_v5"
				pool.Spec.Platform.Azure.Image.Type = hyperv1.AzureMarketplace
				pool.Spec.Platform.Azure.Image.AzureMarketplace = &hyperv1.AzureMarketplaceImage{
					Publisher: "azureopenshift",
					Offer:     "aro4",
					SKU:       "aro_422-arm",
					Version:   "9.8.20260428",
				}
			}
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create ARM64 NodePool %s", np.Name)
		GinkgoWriter.Printf("Created ARM64 NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})
		// Verify the NodePool was created with correct spec
		createdNP := &hyperv1.NodePool{}
		err = testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), createdNP)
		Expect(err).NotTo(HaveOccurred(), "failed to get created ARM64 NodePool %s", np.Name)
		Expect(createdNP.Spec.Arch).To(Equal(hyperv1.ArchitectureARM64), "NodePool should have ARM64 architecture")
		Expect(createdNP.Spec.Replicas).NotTo(BeNil(), "NodePool should have replicas set")
		Expect(*createdNP.Spec.Replicas).To(Equal(int32(1)), "NodePool should have 1 replica")
		if createdNP.Spec.Platform.Type == hyperv1.AWSPlatform {
			Expect(createdNP.Spec.Platform.AWS.InstanceType).To(Equal("m6g.large"), "NodePool should use ARM64-capable instance type")
		}
		if createdNP.Spec.Platform.Type == hyperv1.AzurePlatform {
			Expect(createdNP.Spec.Platform.Azure.VMSize).To(Equal("Standard_D4ps_v5"), "NodePool should use ARM64-capable VM size")
		}
		GinkgoWriter.Printf("Verified ARM64 NodePool %s created with correct architecture settings\n", np.Name)
		// Wait for the ARM64 node to become ready
		nodes := e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)
		Expect(nodes).To(HaveLen(1), "expected exactly 1 ARM64 node for NodePool %s", np.Name)
		// Verify the node has the correct ARM64 architecture label
		node := nodes[0]
		Expect(node.Labels).NotTo(BeNil(), "node %s should have labels", node.Name)
		arch, ok := node.Labels["kubernetes.io/arch"]
		Expect(ok).To(BeTrue(), "node %s should have kubernetes.io/arch label", node.Name)
		Expect(arch).To(Equal("arm64"), "node %s should have ARM64 architecture", node.Name)
		GinkgoWriter.Printf("Verified node %s has ARM64 architecture label\n", node.Name)
	})
}
