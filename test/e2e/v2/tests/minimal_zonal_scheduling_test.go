//go:build e2ev2

/*
Copyright 2026.

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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// controlPlaneOperatorManagedByValue is the value of the
// hypershift.openshift.io/managed-by label that the control-plane-operator
// stamps on the workloads it owns.
const controlPlaneOperatorManagedByValue = "control-plane-operator"

// managedByLabel identifies which operator owns a control plane workload.
const managedByLabel = "hypershift.openshift.io/managed-by"

// workloadManagedBy returns the value of the managed-by label on the workload's
// top-level controller object (Deployment or StatefulSet) and whether that
// object exists. The Minimal-policy scheduling tier is stamped by the
// control-plane-operator only on the components it manages; components deployed
// by other operators (the CNO's ovnkube-control-plane / multus-admission-
// controller / network-node-identity / cloud-network-config-controller and the
// cluster-storage-operator's CSI drivers) are tiered by those operators' own
// images and are out of scope for this control-plane-operator lane.
func workloadManagedBy(testCtx *internal.TestContext, workload internal.WorkloadSpec) (string, bool) {
	var obj crclient.Object
	if workload.Type == "StatefulSet" {
		obj = &appsv1.StatefulSet{}
	} else {
		obj = &appsv1.Deployment{}
	}
	err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: workload.Name}, obj)
	if apierrors.IsNotFound(err) {
		return "", false
	}
	Expect(err).NotTo(HaveOccurred(), "failed to get %s %s/%s", workload.Type, testCtx.ControlPlaneNamespace, workload.Name)
	return obj.GetLabels()[managedByLabel], true
}

func findTopologySpreadConstraint(cs []corev1.TopologySpreadConstraint, topologyKey string) *corev1.TopologySpreadConstraint {
	for i := range cs {
		if cs[i].TopologyKey == topologyKey {
			return &cs[i]
		}
	}
	return nil
}

func nodeAffinityRequiresRole(na *corev1.NodeAffinity, value string) bool {
	if na == nil || na.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false
	}
	for _, term := range na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, req := range term.MatchExpressions {
			if req.Key == hyperv1.ControlPlaneNodeRoleLabel && req.Operator == corev1.NodeSelectorOpIn &&
				len(req.Values) == 1 && req.Values[0] == value {
				return true
			}
		}
	}
	return false
}

func nodeAffinityPrefersRole(na *corev1.NodeAffinity, value string) bool {
	if na == nil {
		return false
	}
	for _, term := range na.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, req := range term.Preference.MatchExpressions {
			if req.Key == hyperv1.ControlPlaneNodeRoleLabel && len(req.Values) == 1 && req.Values[0] == value {
				return true
			}
		}
	}
	return false
}

// RegisterMinimalZonalSchedulingTests registers verification tests for the Minimal
// control plane availability-zone scheduling policy. All tests skip unless the hosted
// cluster is opted into the policy (per the v2 convention that non-lifecycle tests only
// verify existing state and never mutate the hosted cluster to create preconditions).
func RegisterMinimalZonalSchedulingTests(getTestCtx internal.TestContextGetter) {
	Context("Minimal availability-zone scheduling", func() {
		skipUnlessMinimal := func() *internal.TestContext {
			testCtx := getTestCtx()
			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if hc.Spec.ControlPlaneAvailabilityZoneScheduling.Policy != hyperv1.ControlPlaneAvailabilityZoneSchedulingMinimal {
				Skip("HostedCluster is not opted into the Minimal availability-zone scheduling policy")
			}
			return testCtx
		}

		// Per-workload: whatever tier the control-plane-operator stamped on the pod, the
		// generated scheduling must be internally consistent with it.
		for _, w := range workloads {
			workload := w
			if workload.Type != "Deployment" && workload.Type != "StatefulSet" {
				continue
			}
			Context(workload.Name, func() {
				It("should have tier-consistent placement and spreading", func() {
					testCtx := skipUnlessMinimal()

					// Apply the standard platform/version guards so platform-specific
					// workloads that do not run on this cluster are skipped rather than
					// treated as failures.
					hostedCluster, err := testCtx.GetHostedCluster()
					Expect(err).NotTo(HaveOccurred())
					if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
						Skip(fmt.Sprintf("workload %s does not run on this cluster platform", workload.Name))
					}
					testCtx.SkipIfWorkloadUnsupportedForVersion(workload)

					// The Minimal-policy tier label and scheduling are applied by the
					// control-plane-operator to the components it manages. Components owned
					// by other operators (e.g. the CNO's ovnkube-control-plane,
					// multus-admission-controller, network-node-identity and
					// cloud-network-config-controller, or the cluster-storage-operator's
					// CSI drivers) are tiered by those operators' own images, which are
					// pulled from the release payload and may not yet carry this feature.
					// Restrict the assertion to control-plane-operator-managed workloads so
					// this lane reflects what the CPO image can deliver; the cross-repo
					// components are covered by their own operators' tests (e.g. the CNO
					// change for the network components).
					if managedBy, found := workloadManagedBy(testCtx, workload); !found {
						Skip(fmt.Sprintf("workload %s not found in %s", workload.Name, testCtx.ControlPlaneNamespace))
					} else if managedBy != controlPlaneOperatorManagedByValue {
						Skip(fmt.Sprintf("workload %s is managed by %q, not the control-plane-operator; its scheduling tier is applied by that operator's image", workload.Name, managedBy))
					}

					pods := getWorkloadPods(testCtx, workload)
					if len(pods) == 0 {
						// A component may be legitimately absent (e.g. disabled by a
						// capability); skip rather than fail, matching the sibling
						// PodAffinitiesAndTolerationsTest. The loop below never runs on an
						// empty list, so this also avoids a vacuous pass.
						Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
					}

					for i := range pods {
						pod := pods[i]
						tier, ok := pod.Labels[hyperv1.ControlPlaneSchedulingTierLabel]
						Expect(ok).To(BeTrue(), "pod %s is missing the %s label", pod.Name, hyperv1.ControlPlaneSchedulingTierLabel)
						Expect(tier).To(BeElementOf(hyperv1.ControlPlaneNodeRoleZonal, hyperv1.ControlPlaneNodeRoleOverflow),
							"pod %s has unexpected tier %q", pod.Name, tier)

						var na *corev1.NodeAffinity
						if pod.Spec.Affinity != nil {
							na = pod.Spec.Affinity.NodeAffinity
						}
						zoneTSC := findTopologySpreadConstraint(pod.Spec.TopologySpreadConstraints, corev1.LabelTopologyZone)
						if tier == hyperv1.ControlPlaneNodeRoleZonal {
							Expect(nodeAffinityRequiresRole(na, hyperv1.ControlPlaneNodeRoleZonal)).To(BeTrue(),
								"zone-critical pod %s must require the zonal node role", pod.Name)
							if zoneTSC != nil {
								Expect(zoneTSC.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule),
									"zone-critical pod %s must spread hard across zones", pod.Name)
							}
						} else {
							Expect(nodeAffinityRequiresRole(na, hyperv1.ControlPlaneNodeRoleOverflow) ||
								nodeAffinityPrefersRole(na, hyperv1.ControlPlaneNodeRoleOverflow)).To(BeTrue(),
								"float pod %s must target the overflow node role (required or preferred)", pod.Name)
							if zoneTSC != nil {
								Expect(zoneTSC.WhenUnsatisfiable).To(Equal(corev1.ScheduleAnyway),
									"float pod %s must spread best-effort across zones", pod.Name)
							}
						}
					}
				})
			})
		}

		// Anchor assertions catch tier mis-classification and the etcd/pair replica policy.
		// etcd, kube-apiserver, and kube-controller-manager are always present on a
		// HighlyAvailable hosted cluster, so their absence is a failure, not a skip.
		It("keeps etcd as a strict three-replica quorum across zones", func() {
			testCtx := skipUnlessMinimal()
			sts := &appsv1.StatefulSet{}
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: "etcd"}, sts)).
				To(Succeed(), "etcd StatefulSet must be present under the Minimal policy")
			Expect(sts.Spec.Replicas).NotTo(BeNil())
			Expect(*sts.Spec.Replicas).To(Equal(int32(3)), "etcd must run three replicas")
			Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue(hyperv1.ControlPlaneSchedulingTierLabel, hyperv1.ControlPlaneNodeRoleZonal))
			zoneTSC := findTopologySpreadConstraint(sts.Spec.Template.Spec.TopologySpreadConstraints, corev1.LabelTopologyZone)
			Expect(zoneTSC).NotTo(BeNil(), "etcd must have a zone topology spread constraint")
			Expect(zoneTSC.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule))
			Expect(zoneTSC.MinDomains).NotTo(BeNil(), "etcd zone spread must set minDomains")
			Expect(*zoneTSC.MinDomains).To(Equal(int32(3)))
		})

		It("runs kube-apiserver as a zone-critical two-replica pair", func() {
			testCtx := skipUnlessMinimal()
			deploy := &appsv1.Deployment{}
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: "kube-apiserver"}, deploy)).
				To(Succeed(), "kube-apiserver Deployment must be present under the Minimal policy")
			Expect(deploy.Spec.Replicas).NotTo(BeNil())
			Expect(*deploy.Spec.Replicas).To(Equal(int32(2)), "kube-apiserver must run as a two-replica pair under the Minimal policy")
			Expect(deploy.Spec.Template.Labels).To(HaveKeyWithValue(hyperv1.ControlPlaneSchedulingTierLabel, hyperv1.ControlPlaneNodeRoleZonal))
		})

		It("places kube-controller-manager on overflow capacity", func() {
			testCtx := skipUnlessMinimal()
			deploy := &appsv1.Deployment{}
			Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: "kube-controller-manager"}, deploy)).
				To(Succeed(), "kube-controller-manager Deployment must be present under the Minimal policy")
			Expect(deploy.Spec.Template.Labels).To(HaveKeyWithValue(hyperv1.ControlPlaneSchedulingTierLabel, hyperv1.ControlPlaneNodeRoleOverflow))
		})

		It("reports the ControlPlaneAvailabilityZoneSchedulingAvailable condition as True", func() {
			testCtx := skipUnlessMinimal()
			hc, err := testCtx.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			found := false
			for _, cond := range hc.Status.Conditions {
				if cond.Type == string(hyperv1.ControlPlaneAvailabilityZoneSchedulingAvailable) {
					found = true
					Expect(cond.Status).To(Equal(metav1.ConditionTrue),
						"condition %s should be True, reason=%s message=%s", cond.Type, cond.Reason, cond.Message)
					break
				}
			}
			Expect(found).To(BeTrue(), "HostedCluster should report the %s condition", hyperv1.ControlPlaneAvailabilityZoneSchedulingAvailable)
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:MinimalZonalScheduling] Minimal Zonal Scheduling", Label("minimal-zonal-scheduling"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterMinimalZonalSchedulingTests(func() *internal.TestContext { return testCtx })
})
