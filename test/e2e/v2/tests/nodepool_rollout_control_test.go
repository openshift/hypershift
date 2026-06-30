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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

const (
	nodePoolCurrentRolloutConfigAnnotation = "hypershift.openshift.io/nodePoolCurrentRolloutConfig"
	nodePoolCurrentConfigAnnotation        = "hypershift.openshift.io/nodePoolCurrentConfig"
)

func RegisterPredictableRolloutTests(getTestCtx internal.TestContextGetter) {
	ManagementImageChangeNoRolloutTest(getTestCtx)
	SpecDrivenChangeTriggersRolloutTest(getTestCtx)
	OperatorUpgradeNoRolloutTest(getTestCtx)
}

var _ = Describe("Predictable NodePool Rollout", Label("lifecycle", "predictable-rollout"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterPredictableRolloutTests(func() *internal.TestContext { return testCtx })
})

func ManagementImageChangeNoRolloutTest(getTestCtx internal.TestContextGetter) {
	When("a management-side image changes on an existing NodePool", func() {
		It("should NOT trigger a rollout when only the HAProxy image annotation changes", func() {
			testCtx := getTestCtx()
			testCtx.ValidateHostedClusterClient()

			hc := testCtx.GetHostedCluster()
			hcClient := testCtx.GetHostedClusterClient()
			ctx := testCtx.Context

			defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
			Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

			np := &hyperv1.NodePool{}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)).To(Succeed())

			Expect(np.Annotations).To(HaveKey(nodePoolCurrentRolloutConfigAnnotation),
				"NodePool %s should have the rollout config annotation set by the controller", np.Name)
			baselineRolloutHash := np.Annotations[nodePoolCurrentRolloutConfigAnnotation]
			Expect(baselineRolloutHash).NotTo(BeEmpty())

			baselineConfigHash := np.Annotations[nodePoolCurrentConfigAnnotation]

			foundUpdatingConfig := false
			for _, cond := range np.Status.Conditions {
				if cond.Type == hyperv1.NodePoolUpdatingConfigConditionType {
					foundUpdatingConfig = true
					Expect(string(cond.Status)).To(Equal(string(metav1.ConditionFalse)),
						"UpdatingConfig should be False before test mutation")
					break
				}
			}
			Expect(foundUpdatingConfig).To(BeTrue(),
				"NodePool %s should have UpdatingConfig condition", np.Name)

			baselineNodes := &corev1.NodeList{}
			Expect(hcClient.List(ctx, baselineNodes, crclient.MatchingLabels{
				hyperv1.NodePoolLabel: np.Name,
			})).To(Succeed())
			Expect(baselineNodes.Items).NotTo(BeEmpty(),
				"NodePool %s should have at least one ready node", np.Name)
			baselineNodeNames := nodeNames(baselineNodes)

			By("patching the HAProxy image annotation to simulate a management-side image change")
			original := np.DeepCopy()
			if np.Annotations == nil {
				np.Annotations = map[string]string{}
			}
			np.Annotations[hyperv1.NodePoolHAProxyImageAnnotation] = "quay.io/openshift/origin-haproxy-router:e2e-dummy-digest"
			Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
				"failed to patch NodePool %s with HAProxy image annotation", np.Name)
			GinkgoWriter.Printf("Patched NodePool %s with dummy HAProxy image annotation\n", np.Name)

			DeferCleanup(func() {
				fresh := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)
				if apierrors.IsNotFound(err) {
					return
				}
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to get NodePool %s", np.Name)
				patch := fresh.DeepCopy()
				delete(patch.Annotations, hyperv1.NodePoolHAProxyImageAnnotation)
				Expect(testCtx.MgmtClient.Patch(ctx, patch, crclient.MergeFrom(fresh))).To(Succeed(),
					"cleanup: failed to remove HAProxy image annotation from NodePool %s", np.Name)
			})

			By("verifying that no rollout is triggered over 2 minutes")
			Consistently(func(g Gomega) {
				fresh := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)).To(Succeed())

				g.Expect(fresh.Annotations).To(HaveKeyWithValue(
					nodePoolCurrentRolloutConfigAnnotation, baselineRolloutHash),
					"rollout config annotation should not change for management-side image changes")

				for _, cond := range fresh.Status.Conditions {
					if cond.Type == hyperv1.NodePoolUpdatingConfigConditionType {
						g.Expect(string(cond.Status)).To(Equal(string(metav1.ConditionFalse)),
							"UpdatingConfig should remain False — management-side image change must not trigger rollout")
						break
					}
				}

				currentNodes := &corev1.NodeList{}
				g.Expect(hcClient.List(ctx, currentNodes, crclient.MatchingLabels{
					hyperv1.NodePoolLabel: np.Name,
				})).To(Succeed())
				g.Expect(len(currentNodes.Items)).To(Equal(len(baselineNodes.Items)),
					"node count should not change")
			}, 2*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying node identity is unchanged (no replacement)")
			finalNodes := &corev1.NodeList{}
			Expect(hcClient.List(ctx, finalNodes, crclient.MatchingLabels{
				hyperv1.NodePoolLabel: np.Name,
			})).To(Succeed())
			Expect(nodeNames(finalNodes)).To(ConsistOf(baselineNodeNames),
				"node names should be identical — no node replacement should have occurred")

			By("verifying the payload config hash may have changed but rollout hash did not")
			finalNP := &hyperv1.NodePool{}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), finalNP)).To(Succeed())
			Expect(finalNP.Annotations[nodePoolCurrentRolloutConfigAnnotation]).To(Equal(baselineRolloutHash),
				"rollout hash annotation must remain stable")
			_ = baselineConfigHash
		})
	})
}

func SpecDrivenChangeTriggersRolloutTest(getTestCtx internal.TestContextGetter) {
	When("a spec-driven change is made to a NodePool", func() {
		It("should trigger a rollout and update the rollout hash annotation when a MachineConfig is added", func() {
			testCtx := getTestCtx()
			testCtx.ValidateHostedClusterClient()

			hc := testCtx.GetHostedCluster()
			if hc.Spec.Platform.Type == hyperv1.KubevirtPlatform {
				Skip("test is skipped for KubeVirt platform until https://issues.redhat.com/browse/CNV-38196 is addressed")
			}

			hcClient := testCtx.GetHostedClusterClient()
			ctx := testCtx.Context

			defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
			Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

			var oneReplica int32 = 1
			np := buildTestNodePool(defaultNP, "rollout-ctl", func(pool *hyperv1.NodePool) {
				pool.Spec.Replicas = &oneReplica
				pool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
					Strategy: hyperv1.UpgradeStrategyRollingUpdate,
					RollingUpdate: &hyperv1.RollingUpdate{
						MaxUnavailable: ptr.To(intstr.FromInt32(0)),
						MaxSurge:       ptr.To(intstr.FromInt32(oneReplica)),
					},
				}
			})

			err := testCtx.MgmtClient.Create(ctx, np)
			Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
			GinkgoWriter.Printf("Created NodePool %s\n", np.Name)
			DeferCleanup(func() {
				cleanupNodePool(ctx, testCtx.MgmtClient, np)
			})

			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np)).To(Succeed())
			Expect(np.Annotations).To(HaveKey(nodePoolCurrentRolloutConfigAnnotation),
				"NodePool %s should have rollout config annotation after becoming ready", np.Name)
			baselineRolloutHash := np.Annotations[nodePoolCurrentRolloutConfigAnnotation]
			baselineConfigHash := np.Annotations[nodePoolCurrentConfigAnnotation]

			By("creating a MachineConfig and patching the NodePool to reference it")
			ignitionConfig := ignitionapi.Config{
				Ignition: ignitionapi.Ignition{Version: "3.2.0"},
				Storage: ignitionapi.Storage{
					Files: []ignitionapi.File{{
						Node:          ignitionapi.Node{Path: "/etc/predictable-rollout-test"},
						FileEmbedded1: ignitionapi.FileEmbedded1{Contents: ignitionapi.Resource{Source: ptr.To("data:,rollout-test%0A")}},
					}},
				},
			}
			serializedIgnition, err := json.Marshal(ignitionConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to serialize ignition config")

			machineConfig := &mcfgv1.MachineConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "predictable-rollout-test",
					Labels: map[string]string{"machineconfiguration.openshift.io/role": "worker"},
				},
				Spec: mcfgv1.MachineConfigSpec{Config: runtime.RawExtension{Raw: serializedIgnition}},
			}
			gvk, err := apiutil.GVKForObject(machineConfig, hyperapi.Scheme)
			Expect(err).NotTo(HaveOccurred(), "failed to get GVK for MachineConfig")
			machineConfig.SetGroupVersionKind(gvk)

			serializedMC, err := yaml.Marshal(machineConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to serialize MachineConfig")

			mcConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      e2eutil.SimpleNameGenerator.GenerateName("rollout-ctl-mc-"),
					Namespace: hc.Namespace,
				},
				Data: map[string]string{"config": string(serializedMC)},
			}
			Expect(testCtx.MgmtClient.Create(ctx, mcConfigMap)).To(Succeed(), "failed to create MachineConfig ConfigMap")
			DeferCleanup(func() {
				if err := testCtx.MgmtClient.Delete(ctx, mcConfigMap); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", mcConfigMap.Name)
				}
			})
			GinkgoWriter.Printf("Created MachineConfig ConfigMap %s\n", mcConfigMap.Name)

			original := np.DeepCopy()
			np.Spec.Config = append(np.Spec.Config, corev1.LocalObjectReference{Name: mcConfigMap.Name})
			Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
				"failed to patch NodePool %s with MachineConfig", np.Name)

			By("waiting for the rollout to complete")
			e2eutil.WaitForNodePoolConfigUpdateCompleteWithPlatform(GinkgoTB(), ctx, testCtx.MgmtClient, np, hc.Spec.Platform.Type)
			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

			By("verifying the rollout hash annotation was updated")
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np)).To(Succeed())

			Expect(np.Annotations).To(HaveKey(nodePoolCurrentRolloutConfigAnnotation))
			Expect(np.Annotations[nodePoolCurrentRolloutConfigAnnotation]).NotTo(Equal(baselineRolloutHash),
				"rollout config annotation should change after a spec-driven rollout")
			Expect(np.Annotations[nodePoolCurrentRolloutConfigAnnotation]).NotTo(BeEmpty())

			Expect(np.Annotations).To(HaveKey(nodePoolCurrentConfigAnnotation))
			Expect(np.Annotations[nodePoolCurrentConfigAnnotation]).NotTo(Equal(baselineConfigHash),
				"current config annotation should also change after a spec-driven rollout")
			Expect(np.Annotations[nodePoolCurrentConfigAnnotation]).NotTo(BeEmpty())
		})
	})
}

func OperatorUpgradeNoRolloutTest(getTestCtx internal.TestContextGetter) {
	When("the HyperShift operator is upgraded to support rollout hash", func() {
		It("should seed the rollout hash annotation on first reconcile without triggering a rollout", func() {
			testCtx := getTestCtx()
			testCtx.ValidateHostedClusterClient()

			hc := testCtx.GetHostedCluster()
			hcClient := testCtx.GetHostedClusterClient()
			ctx := testCtx.Context

			defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
			Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

			np := &hyperv1.NodePool{}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), np)).To(Succeed())

			Expect(np.Annotations).To(HaveKey(nodePoolCurrentRolloutConfigAnnotation),
				"NodePool %s should have the rollout config annotation before test", np.Name)

			baselineConfigHash := np.Annotations[nodePoolCurrentConfigAnnotation]

			foundUpdatingConfig := false
			for _, cond := range np.Status.Conditions {
				if cond.Type == hyperv1.NodePoolUpdatingConfigConditionType {
					foundUpdatingConfig = true
					Expect(string(cond.Status)).To(Equal(string(metav1.ConditionFalse)),
						"UpdatingConfig should be False before simulating upgrade")
					break
				}
			}
			Expect(foundUpdatingConfig).To(BeTrue(),
				"NodePool %s should have UpdatingConfig condition", np.Name)

			baselineNodes := &corev1.NodeList{}
			Expect(hcClient.List(ctx, baselineNodes, crclient.MatchingLabels{
				hyperv1.NodePoolLabel: np.Name,
			})).To(Succeed())
			Expect(baselineNodes.Items).NotTo(BeEmpty(),
				"NodePool %s should have at least one ready node", np.Name)
			baselineNodeNames := nodeNames(baselineNodes)

			By("removing the rollout config annotation to simulate pre-upgrade state")
			original := np.DeepCopy()
			delete(np.Annotations, nodePoolCurrentRolloutConfigAnnotation)
			Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
				"failed to remove rollout config annotation from NodePool %s", np.Name)
			GinkgoWriter.Printf("Removed rollout config annotation from NodePool %s to simulate pre-upgrade state\n", np.Name)

			By("forcing a reconciliation via a dummy annotation")
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np)).To(Succeed())
			original = np.DeepCopy()
			np.Annotations["hypershift.openshift.io/e2e-reconcile-trigger"] = fmt.Sprintf("%d", time.Now().Unix())
			Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
				"failed to add reconcile trigger annotation to NodePool %s", np.Name)
			DeferCleanup(func() {
				fresh := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)
				if apierrors.IsNotFound(err) {
					return
				}
				Expect(err).NotTo(HaveOccurred())
				patch := fresh.DeepCopy()
				delete(patch.Annotations, "hypershift.openshift.io/e2e-reconcile-trigger")
				Expect(testCtx.MgmtClient.Patch(ctx, patch, crclient.MergeFrom(fresh))).To(Succeed(),
					"cleanup: failed to remove reconcile trigger annotation")
			})

			By("waiting for the controller to re-seed the rollout config annotation")
			Eventually(func(g Gomega) {
				fresh := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)).To(Succeed())
				g.Expect(fresh.Annotations).To(HaveKey(nodePoolCurrentRolloutConfigAnnotation),
					"controller should re-seed the rollout config annotation")
				g.Expect(fresh.Annotations[nodePoolCurrentRolloutConfigAnnotation]).NotTo(BeEmpty())
			}, 1*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying that no rollout was triggered during re-seeding")
			Consistently(func(g Gomega) {
				fresh := &hyperv1.NodePool{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)).To(Succeed())

				for _, cond := range fresh.Status.Conditions {
					if cond.Type == hyperv1.NodePoolUpdatingConfigConditionType {
						g.Expect(string(cond.Status)).To(Equal(string(metav1.ConditionFalse)),
							"UpdatingConfig should remain False — annotation seeding must not trigger rollout")
						break
					}
				}

				g.Expect(fresh.Annotations[nodePoolCurrentConfigAnnotation]).To(Equal(baselineConfigHash),
					"current config annotation should not change during seeding")

				currentNodes := &corev1.NodeList{}
				g.Expect(hcClient.List(ctx, currentNodes, crclient.MatchingLabels{
					hyperv1.NodePoolLabel: np.Name,
				})).To(Succeed())
				g.Expect(len(currentNodes.Items)).To(Equal(len(baselineNodes.Items)),
					"node count should not change during annotation seeding")
			}, 2*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying node identity is unchanged (no replacement)")
			finalNodes := &corev1.NodeList{}
			Expect(hcClient.List(ctx, finalNodes, crclient.MatchingLabels{
				hyperv1.NodePoolLabel: np.Name,
			})).To(Succeed())
			Expect(nodeNames(finalNodes)).To(ConsistOf(baselineNodeNames),
				"node names should be identical — no node replacement should have occurred")
		})
	})
}

func nodeNames(nodeList *corev1.NodeList) []string {
	names := make([]string, len(nodeList.Items))
	for i := range nodeList.Items {
		names[i] = nodeList.Items[i].Name
	}
	return names
}
