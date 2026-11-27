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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			// HO reconcile after deployment. Eventually is justified here because
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
				g.Expect(cond.Reason).To(Equal(hyperv1.NoPressureReason),
					"ManagementClusterResourcePressure reason should be NoPressure on HostedCluster %s/%s", hc.Namespace, hc.Name)
			}).WithContext(tc.Context).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		})
	})
}

// SchedulingFailureInjectionTest injects a simulated scheduling failure by
// directly patching a ControlPlaneComponent's RolloutComplete condition to
// Reason=PodSchedulingFailed and verifying the HO detection chain fires.
//
// This avoids mutating shared management cluster nodes (no taints, no pod
// deletions), so it is safe to run on shared clusters alongside other HCPs.
//
// The detection chain exercised:
//  1. CPC RolloutComplete=False, Reason=PodSchedulingFailed (injected)
//  2. HO CPC watch fires → resource_pressure.go → ManagementClusterResourcePressure=True
//  3. CPC condition reverted → ManagementClusterResourcePressure self-clears to False
func SchedulingFailureInjectionTest(getTestCtx internal.TestContextGetter) {
	It("should detect scheduling pressure from CPC conditions and self-clear after recovery", func() {
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

		By("finding a target CPC to inject the failure condition")
		cpcList := &hyperv1.ControlPlaneComponentList{}
		Expect(tc.MgmtClient.List(ctx, cpcList, crclient.InNamespace(cpNamespace))).To(Succeed())
		Expect(cpcList.Items).NotTo(BeEmpty(), "expected CPCs in control plane namespace %s", cpNamespace)

		var targetCPC *hyperv1.ControlPlaneComponent
		for i := range cpcList.Items {
			cpc := &cpcList.Items[i]
			// Skip CPO and etcd to avoid disrupting cluster operations.
			if cpc.Name == "control-plane-operator" || cpc.Name == "etcd" {
				continue
			}
			targetCPC = cpc
			break
		}
		Expect(targetCPC).NotTo(BeNil(), "expected at least one non-CPO, non-etcd CPC in %s", cpNamespace)

		// Save original conditions for cleanup.
		originalConditions := make([]metav1.Condition, len(targetCPC.Status.Conditions))
		copy(originalConditions, targetCPC.Status.Conditions)

		DeferCleanup(func(ctx context.Context) {
			// Restore the original conditions on the CPC, tolerating only an already-absent resource.
			Eventually(func(g Gomega) {
				fresh := &hyperv1.ControlPlaneComponent{}
				err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(targetCPC), fresh)
				if apierrors.IsNotFound(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to get CPC %s/%s during cleanup", targetCPC.Namespace, targetCPC.Name)
				patch := crclient.MergeFrom(fresh.DeepCopy())
				fresh.Status.Conditions = originalConditions
				g.Expect(tc.MgmtClient.Status().Patch(ctx, fresh, patch)).To(Succeed(), "failed to restore CPC %s/%s conditions during cleanup", targetCPC.Namespace, targetCPC.Name)
			}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).Should(Succeed())
		})

		cpcKey := crclient.ObjectKeyFromObject(targetCPC)
		injectSchedulingFailure := func(g Gomega) {
			fresh := &hyperv1.ControlPlaneComponent{}
			g.Expect(tc.MgmtClient.Get(ctx, cpcKey, fresh)).To(Succeed())
			rollout := meta.FindStatusCondition(fresh.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
			if rollout != nil && rollout.Status == metav1.ConditionFalse && rollout.Reason == hyperv1.PodSchedulingFailedReason {
				return
			}
			patch := crclient.MergeFrom(fresh.DeepCopy())
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
				Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.PodSchedulingFailedReason,
				Message: "Injected by installation-diagnostics e2e test",
			})
			g.Expect(tc.MgmtClient.Status().Patch(ctx, fresh, patch)).To(Succeed())
		}

		By("injecting PodSchedulingFailed on the target CPC's RolloutComplete condition")
		injectSchedulingFailure(Default)

		By("waiting for ManagementClusterResourcePressure to become True")
		Eventually(func(g Gomega) {
			hc := &hyperv1.HostedCluster{}
			g.Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			if cond != nil && cond.Status == metav1.ConditionTrue {
				return
			}
			// CPO may reconcile the healthy target CPC and revert RolloutComplete
			// before HO observes it or between polls; re-apply the injected condition
			// until HO surfaces ManagementClusterResourcePressure=True.
			injectSchedulingFailure(g)
			g.Expect(cond).NotTo(BeNil(),
				"ManagementClusterResourcePressure condition should be present on HostedCluster")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"ManagementClusterResourcePressure should be True after CPC condition injection")
		}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(1 * time.Second).Should(Succeed())

		By("reverting the CPC condition to simulate recovery")
		fresh := &hyperv1.ControlPlaneComponent{}
		Expect(tc.MgmtClient.Get(ctx, cpcKey, fresh)).To(Succeed())
		revertPatch := crclient.MergeFrom(fresh.DeepCopy())
		fresh.Status.Conditions = originalConditions
		Expect(tc.MgmtClient.Status().Patch(ctx, fresh, revertPatch)).To(Succeed())

		By("waiting for ManagementClusterResourcePressure to self-clear to False")
		Eventually(func(g Gomega) {
			hc := &hyperv1.HostedCluster{}
			g.Expect(tc.MgmtClient.Get(ctx, hcKey, hc)).To(Succeed())
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ManagementClusterResourcePressure))
			g.Expect(cond).NotTo(BeNil(),
				"ManagementClusterResourcePressure condition should be present on HostedCluster")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse),
				"ManagementClusterResourcePressure should self-clear to False after recovery")
			g.Expect(cond.Reason).To(Equal(hyperv1.NoPressureReason),
				"ManagementClusterResourcePressure reason should be NoPressure after recovery")
		}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())
	})
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
