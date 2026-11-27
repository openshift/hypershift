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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterPlatformDiagnosticsTests registers platform-specific installation
// diagnostic enrichment tests for AWS and Azure.
func RegisterPlatformDiagnosticsTests(getTestCtx internal.TestContextGetter) {
	ValidateAWSNodePoolIgnitionDiagnosticHintTest(getTestCtx)
	ValidateAzureNodePoolIgnitionDiagnosticHintTest(getTestCtx)
}

// ValidateAWSNodePoolIgnitionDiagnosticHintTest creates a zero-replica NodePool
// on an AWS HostedCluster (no EC2 instances are provisioned) and verifies that
// NodePoolReachedIgnitionEndpoint=False (Reason=IgnitionNotReached) keeps a
// neutral message without networking troubleshooting advice since no workers
// are expected.
func ValidateAWSNodePoolIgnitionDiagnosticHintTest(getTestCtx internal.TestContextGetter) {
	When("a zero-replica AWS NodePool has not reached the ignition endpoint", Label("AWS"), func() {
		It("should report a neutral IgnitionNotReached message without troubleshooting guidance", func() {
			verifyZeroReplicaNodePoolNeutralIgnitionMessage(getTestCtx(), hyperv1.AWSPlatform)
		})
	})
}

// ValidateAzureNodePoolIgnitionDiagnosticHintTest creates a zero-replica NodePool
// on an Azure HostedCluster (no Azure VMs are provisioned) and verifies that
// NodePoolReachedIgnitionEndpoint=False (Reason=IgnitionNotReached) keeps a
// neutral message without networking troubleshooting advice since no workers
// are expected.
func ValidateAzureNodePoolIgnitionDiagnosticHintTest(getTestCtx internal.TestContextGetter) {
	When("a zero-replica Azure NodePool has not reached the ignition endpoint", Label("Azure"), func() {
		It("should report a neutral IgnitionNotReached message without troubleshooting guidance", func() {
			verifyZeroReplicaNodePoolNeutralIgnitionMessage(getTestCtx(), hyperv1.AzurePlatform)
		})
	})
}

func verifyZeroReplicaNodePoolNeutralIgnitionMessage(tc *internal.TestContext, requiredPlatform hyperv1.PlatformType) {
	GinkgoHelper()

	hc, err := tc.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", tc.ClusterNamespace, tc.ClusterName)
	if hc.Spec.Platform.Type != requiredPlatform {
		Skip("test requires " + string(requiredPlatform) + " platform")
	}

	ctx := tc.Context
	defaultNP := getDefaultNodePool(ctx, tc.MgmtClient, hc)
	Expect(defaultNP).NotTo(BeNil(), "expected a default NodePool for HostedCluster %s/%s", hc.Namespace, hc.Name)

	np := buildTestNodePool(defaultNP, "diag-hint", func(pool *hyperv1.NodePool) {
		pool.Spec.Replicas = ptr.To[int32](0)
		pool.Spec.AutoScaling = nil
		pool.Spec.Release.Image = hc.Spec.Release.Image
	})

	By("creating a zero-replica NodePool to observe IgnitionNotReached diagnostics")
	Expect(tc.MgmtClient.Create(ctx, np)).To(Succeed(),
		"failed to create zero-replica NodePool %s/%s", np.Namespace, np.Name)

	DeferCleanup(func(cleanupCtx context.Context) {
		err := tc.MgmtClient.Delete(cleanupCtx, np)
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred(), "failed to delete temporary NodePool %s/%s", np.Namespace, np.Name)
		Eventually(func(g Gomega) {
			fresh := &hyperv1.NodePool{}
			getErr := tc.MgmtClient.Get(cleanupCtx, crclient.ObjectKeyFromObject(np), fresh)
			g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(),
				"expected temporary NodePool %s/%s to be deleted", np.Namespace, np.Name)
		}).WithContext(cleanupCtx).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	By("waiting for NodePoolReachedIgnitionEndpoint=False with a neutral message")
	npKey := crclient.ObjectKeyFromObject(np)
	Eventually(func(g Gomega) {
		expectNodePoolIgnitionNotReachedNeutral(g, tc, npKey)
	}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}

func expectNodePoolIgnitionNotReachedNeutral(g Gomega, tc *internal.TestContext, npKey crclient.ObjectKey) {
	GinkgoHelper()

	fresh := &hyperv1.NodePool{}
	g.Expect(tc.MgmtClient.Get(tc.Context, npKey, fresh)).To(Succeed(),
		"failed to get NodePool %s/%s", npKey.Namespace, npKey.Name)

	var cond *hyperv1.NodePoolCondition
	for i := range fresh.Status.Conditions {
		if fresh.Status.Conditions[i].Type == hyperv1.NodePoolReachedIgnitionEndpoint {
			cond = &fresh.Status.Conditions[i]
			break
		}
	}
	g.Expect(cond).NotTo(BeNil(),
		"NodePoolReachedIgnitionEndpoint condition should be present on NodePool %s/%s", npKey.Namespace, npKey.Name)
	g.Expect(cond.Status).To(Equal(corev1.ConditionFalse),
		"NodePoolReachedIgnitionEndpoint should be False on zero-replica NodePool %s/%s", npKey.Namespace, npKey.Name)
	g.Expect(cond.Reason).To(Equal(hyperv1.IgnitionNotReached),
		"NodePoolReachedIgnitionEndpoint reason should be IgnitionNotReached on NodePool %s/%s", npKey.Namespace, npKey.Name)
	g.Expect(cond.Message).To(Equal("Nodes have not reached the ignition endpoint."),
		"NodePoolReachedIgnitionEndpoint message on zero-replica NodePool %s/%s should stay neutral without troubleshooting advice", npKey.Namespace, npKey.Name)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:InstallationDiagnostics] Platform Installation Diagnostics",
	Label("installation-diagnostics", "lifecycle", internal.InformingLabel), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		RegisterPlatformDiagnosticsTests(func() *internal.TestContext { return testCtx })
	})
