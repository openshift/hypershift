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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterInstallationDiagnosticsTests registers all installation diagnostics test specs.
func RegisterInstallationDiagnosticsTests(getTestCtx internal.TestContextGetter) {
	ValidateInstallationStageCompleteTest(getTestCtx)
	ValidateManagementClusterResourcePressureTest(getTestCtx)
}

// ValidateInstallationStageCompleteTest verifies that a fully installed hosted cluster
// has its installation stage set to InstallationComplete on the HostedCluster,
// and that the lastTransitionTime is populated.
func ValidateInstallationStageCompleteTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster is fully installed", func() {
		It("should have installation stage set to InstallationComplete", func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			// The installation stage is a new feature that requires at least one
			// CPO reconcile after deployment. Eventually is justified here because
			// we are waiting for an asynchronous condition to converge.
			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				g.Expect(string(hc.Status.Installation.Stage)).NotTo(BeEmpty(),
					"installation stage should be set on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(hc.Status.Installation.Stage).To(Equal(hyperv1.InstallationStageInstallationComplete),
					"installation stage should be InstallationComplete on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(hc.Status.Installation.LastTransitionTime.IsZero()).To(BeFalse(),
					"installation lastTransitionTime should be set on HostedCluster %s/%s", hc.Namespace, hc.Name)
			}).WithContext(tc.Context).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

// ValidateManagementClusterResourcePressureTest verifies that a healthy hosted cluster
// has the ManagementClusterResourcePressure condition set to False with reason NoPressure.
func ValidateManagementClusterResourcePressureTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has no scheduling pressure", func() {
		It("should have ManagementClusterResourcePressure set to False", func() {
			tc := getTestCtx()
			hostedCluster, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			// The condition requires at least one HO reconcile after deployment.
			// A short timeout suffices since this is a single-reconcile convergence.
			Eventually(func(g Gomega) {
				hc := &hyperv1.HostedCluster{}
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
				cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
				g.Expect(cond).NotTo(BeNil(),
					"ManagementClusterResourcePressure condition should be present on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
					"ManagementClusterResourcePressure should be False on HostedCluster %s/%s", hc.Namespace, hc.Name)
				g.Expect(cond.Reason).To(Equal(hyperv1.ReasonNoPressure),
					"ManagementClusterResourcePressure reason should be NoPressure on HostedCluster %s/%s", hc.Namespace, hc.Name)
			}).WithContext(tc.Context).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

// SchedulingFailureInjectionTest injects a scheduling failure by tainting only
// the nodes where this hosted cluster's control plane pods run, then deleting
// a control plane pod so its replacement cannot schedule.
//
// Only nodes hosting pods from this HC's control plane namespace are tainted,
// limiting blast radius to this cluster. Pods already running on those nodes
// are unaffected by NoSchedule — only new scheduling attempts are blocked.
//
// The full detection chain exercised:
//  1. Pod pending with PodScheduled=False, Reason=Unschedulable
//  2. CPO reconciles → CPC RolloutComplete=False, Reason=PodSchedulingFailed
//  3. HO CPC watch fires → resource_pressure.go → ManagementClusterResourcePressure=True
func SchedulingFailureInjectionTest(getTestCtx internal.TestContextGetter) {
	It("should detect scheduling pressure and self-clear after recovery", func() {
		tc := getTestCtx()
		ctx := tc.Context
		cpNamespace := tc.ControlPlaneNamespace
		hcKey := crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}

		By("verifying ManagementClusterResourcePressure is initially False")
		hc := &hyperv1.HostedCluster{}
		Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
		cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
		Expect(cond).NotTo(BeNil(), "ManagementClusterResourcePressure should exist before injection")
		Expect(cond.Status).To(Equal(metav1.ConditionFalse), "ManagementClusterResourcePressure should be False before injection")

		By("listing control plane pods to find target and node set")
		podList := &corev1.PodList{}
		Expect(tc.MgmtClient.List(ctx, podList, crclient.InNamespace(cpNamespace))).To(Succeed())
		Expect(podList.Items).NotTo(BeEmpty(), "expected pods in control plane namespace %s", cpNamespace)

		// Pick a running pod owned by a ControlPlaneComponent (excluding
		// control-plane-operator and etcd) so the CPO stays alive to set
		// PodSchedulingFailed on the CPC condition.
		cpcList := &hyperv1.ControlPlaneComponentList{}
		Expect(tc.MgmtClient.List(ctx, cpcList, crclient.InNamespace(cpNamespace))).To(Succeed())
		isTargetComponentPod := func(podName string) bool {
			for _, cpc := range cpcList.Items {
				if cpc.Name == "control-plane-operator" || cpc.Name == "etcd" {
					continue
				}
				if strings.HasPrefix(podName, cpc.Name+"-") {
					return true
				}
			}
			return false
		}

		// Collect the set of nodes running this HC's control plane pods
		// and find a target pod for deletion.
		cpNodeNames := make(map[string]struct{})
		var targetPod *corev1.Pod
		for i := range podList.Items {
			p := &podList.Items[i]
			if p.Spec.NodeName != "" {
				cpNodeNames[p.Spec.NodeName] = struct{}{}
			}
			if targetPod == nil && p.Status.Phase == corev1.PodRunning && isTargetComponentPod(p.Name) {
				targetPod = p
			}
		}
		Expect(targetPod).NotTo(BeNil(), "expected at least one running CPC-owned pod in %s", cpNamespace)
		Expect(cpNodeNames).NotTo(BeEmpty(), "expected at least one node running control plane pods")

		By("tainting only nodes hosting this cluster's control plane pods")
		taintKey := "installation-diagnostics-e2e/block-scheduling"
		taint := corev1.Taint{
			Key:    taintKey,
			Value:  "true",
			Effect: corev1.TaintEffectNoSchedule,
		}

		// Register cleanup before tainting so partial failures still get cleaned up.
		DeferCleanup(func(ctx context.Context) {
			removeTaintFromNodes(ctx, tc.MgmtClient, taintKey)
		})

		nodeList := &corev1.NodeList{}
		Expect(tc.MgmtClient.List(ctx, nodeList)).To(Succeed())
		Expect(nodeList.Items).NotTo(BeEmpty(), "expected at least one node in the management cluster")
		var taintedCount int
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			if _, ok := cpNodeNames[node.Name]; !ok {
				continue
			}
			patch := crclient.MergeFrom(node.DeepCopy())
			node.Spec.Taints = append(node.Spec.Taints, taint)
			Expect(tc.MgmtClient.Patch(ctx, node, patch)).To(Succeed())
			taintedCount++
		}
		Expect(taintedCount).To(BeNumerically(">", 0), "expected to taint at least one node")

		By("deleting a control plane pod to trigger rescheduling failure")
		Expect(tc.MgmtClient.Delete(ctx, targetPod)).To(Succeed())

		By("waiting for ManagementClusterResourcePressure to become True")
		Eventually(func(g Gomega) {
			hc := &hyperv1.HostedCluster{}
			g.Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			g.Expect(cond).NotTo(BeNil(),
				"ManagementClusterResourcePressure condition should be present on HostedCluster")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"ManagementClusterResourcePressure should be True after scheduling failure injection")
		}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("removing taints to allow recovery")
		removeTaintFromNodes(ctx, tc.MgmtClient, taintKey)

		By("waiting for ManagementClusterResourcePressure to self-clear to False")
		Eventually(func(g Gomega) {
			hc := &hyperv1.HostedCluster{}
			g.Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			g.Expect(cond).NotTo(BeNil(),
				"ManagementClusterResourcePressure condition should be present on HostedCluster")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
				"ManagementClusterResourcePressure should self-clear to False after recovery")
			g.Expect(cond.Reason).To(Equal(hyperv1.ReasonNoPressure),
				"ManagementClusterResourcePressure reason should be NoPressure after recovery")
		}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})
}

// removeTaintFromNodes removes a taint with the given key from all nodes that
// have it. It is idempotent: nodes without the taint are skipped.
func removeTaintFromNodes(ctx context.Context, c crclient.Client, taintKey string) {
	nodeList := &corev1.NodeList{}
	ExpectWithOffset(1, c.List(ctx, nodeList)).To(Succeed())
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		hasTaint := false
		for _, t := range node.Spec.Taints {
			if t.Key == taintKey {
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			continue
		}
		// Re-fetch to avoid conflicts from stale resourceVersion.
		fresh := &corev1.Node{}
		ExpectWithOffset(1, c.Get(ctx, crclient.ObjectKeyFromObject(node), fresh)).To(Succeed())
		patch := crclient.MergeFrom(fresh.DeepCopy())
		var filtered []corev1.Taint
		for _, t := range fresh.Spec.Taints {
			if t.Key != taintKey {
				filtered = append(filtered, t)
			}
		}
		fresh.Spec.Taints = filtered
		ExpectWithOffset(1, c.Patch(ctx, fresh, patch)).To(Succeed())
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:InstallationDiagnostics] Installation Diagnostics",
	Label("installation-diagnostics", internal.InformingLabel), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		RegisterInstallationDiagnosticsTests(func() *internal.TestContext { return testCtx })
	})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:InstallationDiagnostics] Installation Diagnostics Failure Injection",
	Label("installation-diagnostics", "lifecycle", internal.InformingLabel), Serial, func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
		})

		SchedulingFailureInjectionTest(func() *internal.TestContext { return testCtx })
	})
