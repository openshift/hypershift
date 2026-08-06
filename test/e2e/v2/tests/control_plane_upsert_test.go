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
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var desiredStateHashHexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func RegisterUpsertTests(getTestCtx internal.TestContextGetter) {
	VerifyDesiredStateHashAnnotationStamped(getTestCtx)
	VerifyDesiredStateHashIdempotency(getTestCtx)
	VerifySpecFieldRemovalDetected(getTestCtx)
	VerifyExternalDriftReverted(getTestCtx)
	VerifyServiceAccountPullSecretsPreserved(getTestCtx)
}

// VerifyDesiredStateHashAnnotationStamped verifies Scenario 1: every managed Deployment in the
// control plane namespace carries a valid 64-char hex desired-state-hash annotation.
func VerifyDesiredStateHashAnnotationStamped(getTestCtx internal.TestContextGetter) {
	Context("managed Deployments carry desired-state-hash annotation", func() {
		It("should have a valid 64-char hex desired-state-hash annotation on all managed Deployments", func() {
			tc := getTestCtx()

			deployList := &appsv1.DeploymentList{}
			Expect(tc.MgmtClient.List(tc.Context, deployList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to list Deployments in namespace %s", tc.ControlPlaneNamespace)
			Expect(deployList.Items).NotTo(BeEmpty(),
				"expected at least one managed Deployment in namespace %s", tc.ControlPlaneNamespace)

			for i := range deployList.Items {
				deploy := &deployList.Items[i]
				hash, ok := deploy.Annotations[upsert.DesiredStateHashAnnotation]
				Expect(ok).To(BeTrue(),
					"Deployment %s/%s is missing the %s annotation",
					deploy.Namespace, deploy.Name, upsert.DesiredStateHashAnnotation)
				Expect(desiredStateHashHexRE.MatchString(hash)).To(BeTrue(),
					"Deployment %s/%s has invalid desired-state-hash %q (expected 64 hex chars)",
					deploy.Namespace, deploy.Name, hash)
			}
		})
	})
}

// VerifyDesiredStateHashIdempotency verifies Scenario 3: a stable cluster produces no repeated
// no-op updates — managed Deployment resourceVersions do not change across multiple reconcile cycles.
func VerifyDesiredStateHashIdempotency(getTestCtx internal.TestContextGetter) {
	Context("reconcile idempotency on stable cluster", func() {
		It("should not update managed Deployments when cluster state is stable", func() {
			tc := getTestCtx()

			deployList := &appsv1.DeploymentList{}
			Expect(tc.MgmtClient.List(tc.Context, deployList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to list Deployments in namespace %s", tc.ControlPlaneNamespace)
			Expect(deployList.Items).NotTo(BeEmpty(),
				"expected at least one managed Deployment in namespace %s", tc.ControlPlaneNamespace)

			initialVersions := make(map[string]string, len(deployList.Items))
			for i := range deployList.Items {
				d := &deployList.Items[i]
				initialVersions[d.Name] = d.ResourceVersion
			}

			// Wait long enough to cover at least two reconcile cycles.
			time.Sleep(2 * time.Minute)

			updatedList := &appsv1.DeploymentList{}
			Expect(tc.MgmtClient.List(tc.Context, updatedList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to re-list Deployments in namespace %s after wait", tc.ControlPlaneNamespace)

			for i := range updatedList.Items {
				d := &updatedList.Items[i]
				initialVersion, existed := initialVersions[d.Name]
				if !existed {
					continue // new Deployment appeared during the wait; skip
				}
				Expect(d.ResourceVersion).To(Equal(initialVersion),
					"Deployment %s/%s had unexpected resourceVersion change — possible reconcile hot-loop",
					d.Namespace, d.Name)
			}
		})
	})
}

// VerifySpecFieldRemovalDetected verifies Scenario 2: removing a field from HostedCluster spec
// propagates to managed Deployments. Before PR #7713, DeepDerivative(desired, current) would
// return true when the desired toleration slice is a prefix of the current slice — silently
// missing the extra toleration. The desired-state-hash catches this because the hash of the
// desired manifest changes as soon as the toleration is removed from HC spec.
func VerifySpecFieldRemovalDetected(getTestCtx internal.TestContextGetter) {
	Context("spec field removal is detected and applied", func() {
		It("should remove a toleration from managed Deployments when removed from HostedCluster spec", func() {
			tc := getTestCtx()

			// A toleration for a non-existent taint key is completely harmless: pods tolerate
			// taints that no node will ever actually have.
			testToleration := corev1.Toleration{
				Key:      "hypershift.openshift.io/e2e-upsert-removal-test",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}

			deployList := &appsv1.DeploymentList{}
			Expect(tc.MgmtClient.List(tc.Context, deployList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to list Deployments in namespace %s", tc.ControlPlaneNamespace)
			Expect(deployList.Items).NotTo(BeEmpty(),
				"expected at least one managed Deployment in namespace %s", tc.ControlPlaneNamespace)
			targetDeployKey := crclient.ObjectKeyFromObject(&deployList.Items[0])

			hcKey := crclient.ObjectKeyFromObject(tc.GetHostedCluster())
			getHC := func() *hyperv1.HostedCluster {
				hc := &hyperv1.HostedCluster{}
				Expect(tc.MgmtClient.Get(tc.Context, hcKey, hc)).To(Succeed(),
					"failed to fetch HostedCluster %s", hcKey)
				return hc
			}
			removeToleration := func(tols []corev1.Toleration) []corev1.Toleration {
				out := tols[:0:0]
				for _, t := range tols {
					if t.Key != testToleration.Key {
						out = append(out, t)
					}
				}
				return out
			}
			hasToleration := func(deploy *appsv1.Deployment) bool {
				for _, t := range deploy.Spec.Template.Spec.Tolerations {
					if t.Key == testToleration.Key {
						return true
					}
				}
				return false
			}

			// Step 1: add the test toleration to HC spec.
			hc := getHC()
			base := hc.DeepCopy()
			hc.Spec.Tolerations = append(hc.Spec.Tolerations, testToleration)
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.MergeFrom(base))).To(Succeed(),
				"failed to patch HostedCluster %s to add test toleration", hcKey)

			// DeferCleanup removes the test toleration on all exit paths.
			DeferCleanup(func() {
				current := getHC()
				cleaned := removeToleration(current.Spec.Tolerations)
				if len(cleaned) == len(current.Spec.Tolerations) {
					return // already removed
				}
				restore := current.DeepCopy()
				current.Spec.Tolerations = cleaned
				if err := tc.MgmtClient.Patch(tc.Context, current, crclient.MergeFrom(restore)); err != nil {
					if !apierrors.IsNotFound(err) {
						GinkgoLogr.Error(err, "cleanup: failed to remove test toleration from HostedCluster", "key", hcKey)
					}
				}
			})

			// Step 2: wait for the toleration to propagate HC → HCP → Deployment (two reconcile hops).
			var hashWithToleration string
			Eventually(func(g Gomega) {
				deploy := &appsv1.Deployment{}
				g.Expect(tc.MgmtClient.Get(tc.Context, targetDeployKey, deploy)).To(Succeed())
				g.Expect(hasToleration(deploy)).To(BeTrue(),
					"test toleration not yet present on Deployment %s", targetDeployKey)
				hashWithToleration = deploy.Annotations[upsert.DesiredStateHashAnnotation]
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			// Step 3: remove the test toleration from HC spec.
			hc = getHC()
			base = hc.DeepCopy()
			hc.Spec.Tolerations = removeToleration(hc.Spec.Tolerations)
			Expect(tc.MgmtClient.Patch(tc.Context, hc, crclient.MergeFrom(base))).To(Succeed(),
				"failed to patch HostedCluster %s to remove test toleration", hcKey)

			// Step 4: wait for the toleration to disappear from the Deployment.
			// DeepDerivative(desired_without_tol, current_with_tol) returns true because the
			// desired slice is a prefix of the current slice — without the hash fix this removal
			// would never be applied and this Eventually would time out.
			Eventually(func(g Gomega) {
				deploy := &appsv1.Deployment{}
				g.Expect(tc.MgmtClient.Get(tc.Context, targetDeployKey, deploy)).To(Succeed())
				g.Expect(hasToleration(deploy)).To(BeFalse(),
					"test toleration still present on Deployment %s — the desired-state-hash "+
						"mechanism should have detected its removal from HC spec",
					targetDeployKey)
				g.Expect(deploy.Annotations[upsert.DesiredStateHashAnnotation]).NotTo(Equal(hashWithToleration),
					"expected desired-state-hash to change on Deployment %s after toleration removal",
					targetDeployKey)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

// VerifyExternalDriftReverted verifies Scenario 5: out-of-band spec mutations to a managed
// Deployment are detected via the DeepDerivative fallback and reverted by the reconciler.
func VerifyExternalDriftReverted(getTestCtx internal.TestContextGetter) {
	Context("external drift is corrected by DeepDerivative fallback", Label("Informing"), func() {
		It("should revert an out-of-band replica count change on a managed Deployment", func() {
			tc := getTestCtx()

			deployList := &appsv1.DeploymentList{}
			Expect(tc.MgmtClient.List(tc.Context, deployList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to list Deployments in namespace %s", tc.ControlPlaneNamespace)
			Expect(deployList.Items).NotTo(BeEmpty(),
				"expected at least one managed Deployment in namespace %s", tc.ControlPlaneNamespace)

			target := deployList.Items[0].DeepCopy()
			targetKey := crclient.ObjectKeyFromObject(target)

			originalReplicas := int32(1)
			if target.Spec.Replicas != nil {
				originalReplicas = *target.Spec.Replicas
			}
			originalHash := target.Annotations[upsert.DesiredStateHashAnnotation]
			patchedReplicas := originalReplicas + 1

			// Patch replicas out-of-band — the desired manifest is unchanged so the hash still
			// matches; only the DeepDerivative fallback can catch and revert this drift.
			base := target.DeepCopy()
			target.Spec.Replicas = ptr.To(patchedReplicas)
			Expect(tc.MgmtClient.Patch(tc.Context, target, crclient.MergeFrom(base))).
				To(Succeed(), "failed to patch replicas on Deployment %s", targetKey)

			DeferCleanup(func() {
				current := &appsv1.Deployment{}
				if err := tc.MgmtClient.Get(tc.Context, targetKey, current); err != nil {
					if !apierrors.IsNotFound(err) {
						GinkgoLogr.Error(err, "cleanup: failed to get Deployment", "key", targetKey)
					}
					return
				}
				if current.Spec.Replicas != nil && *current.Spec.Replicas == originalReplicas {
					return // already reverted by the reconciler
				}
				restore := current.DeepCopy()
				current.Spec.Replicas = ptr.To(originalReplicas)
				if err := tc.MgmtClient.Patch(tc.Context, current, crclient.MergeFrom(restore)); err != nil {
					if !apierrors.IsNotFound(err) {
						GinkgoLogr.Error(err, "cleanup: failed to restore replicas on Deployment", "key", targetKey)
					}
				}
			})

			Eventually(func(g Gomega) {
				current := &appsv1.Deployment{}
				g.Expect(tc.MgmtClient.Get(tc.Context, targetKey, current)).
					To(Succeed(), "failed to fetch Deployment %s", targetKey)
				g.Expect(current.Spec.Replicas).NotTo(BeNil(),
					"Deployment %s spec.replicas should not be nil", targetKey)
				g.Expect(*current.Spec.Replicas).To(Equal(originalReplicas),
					"expected reconciler to revert replica count from %d back to %d on Deployment %s",
					patchedReplicas, originalReplicas, targetKey)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			// The desired manifest did not change, so the hash must remain identical.
			// The revert was driven solely by the DeepDerivative fallback.
			final := &appsv1.Deployment{}
			Expect(tc.MgmtClient.Get(tc.Context, targetKey, final)).To(Succeed())
			Expect(final.Annotations[upsert.DesiredStateHashAnnotation]).To(Equal(originalHash),
				"desired-state-hash changed unexpectedly on Deployment %s — drift should have been caught by DeepDerivative, not the hash path",
				targetKey)
		})
	})
}

// VerifyServiceAccountPullSecretsPreserved verifies Scenario 8b: the reconciler does not wipe
// imagePullSecrets injected by Kubernetes into ServiceAccounts in the control plane namespace.
func VerifyServiceAccountPullSecretsPreserved(getTestCtx internal.TestContextGetter) {
	Context("ServiceAccount imagePullSecrets are preserved across reconciles", func() {
		It("should not clear imagePullSecrets on managed ServiceAccounts", func() {
			tc := getTestCtx()

			saList := &corev1.ServiceAccountList{}
			Expect(tc.MgmtClient.List(tc.Context, saList, crclient.InNamespace(tc.ControlPlaneNamespace))).
				To(Succeed(), "failed to list ServiceAccounts in namespace %s", tc.ControlPlaneNamespace)
			Expect(saList.Items).NotTo(BeEmpty(),
				"expected at least one ServiceAccount in namespace %s", tc.ControlPlaneNamespace)

			for i := range saList.Items {
				sa := &saList.Items[i]
				Expect(sa.ImagePullSecrets).NotTo(BeEmpty(),
					"ServiceAccount %s/%s has empty imagePullSecrets — the reconciler may have wiped Kubernetes-injected pull secrets",
					sa.Namespace, sa.Name)
			}
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:UpsertDesiredStateHash] Upsert Desired State Hash",
	Label("upsert-desired-state-hash"), func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context must be initialized")
			testCtx.ValidateHostedCluster()
		})

		RegisterUpsertTests(func() *internal.TestContext { return testCtx })
	})
