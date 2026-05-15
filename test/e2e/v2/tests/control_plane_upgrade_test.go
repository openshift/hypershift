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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ControlPlaneUpgradeTest registers tests for control plane upgrade lifecycle.
func ControlPlaneUpgradeTest(getTestCtx internal.TestContextGetter) {
	Context("Control Plane Upgrade", func() {
		It("should upgrade the control plane to the latest release image", func() {
			testCtx := getTestCtx()
			ctx := testCtx.Context
			hostedCluster := testCtx.GetHostedCluster()
			Expect(hostedCluster).NotTo(BeNil(), "hosted cluster should be available")

			latestReleaseImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
			Expect(latestReleaseImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")

			// Record the starting version from the current version history.
			var startingVersion string
			if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
				startingVersion = hostedCluster.Status.Version.History[0].Version
			}
			GinkgoLogr.Info("Starting control plane upgrade",
				"startingVersion", startingVersion,
				"targetImage", latestReleaseImage,
			)

			// Capture the last completion time before the upgrade so the data plane
			// rollout predicate can detect when a *new* history entry completes.
			var lastVersionCompletionTime *metav1.Time
			if hostedCluster.Status.Version != nil && len(hostedCluster.Status.Version.History) > 0 {
				lastVersionCompletionTime = hostedCluster.Status.Version.History[0].CompletionTime
			}

			// Update the hosted cluster release image and set ForceUpgradeToAnnotation.
			// UpdateObject takes testing.TB so GinkgoTB() works here.
			err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = latestReleaseImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = latestReleaseImage
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster release image")

			// Step 1: Wait for ControlPlaneComponent resources to complete rollout (4.20+).
			// Inlined from e2eutil.WaitForControlPlaneComponentRollout because the v1
			// wrapper takes *testing.T which is incompatible with GinkgoTB().
			By("Waiting for control plane components to complete rollout")
			e2eutil.GinkgoAtLeast(e2eutil.Version420)
			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "control plane components to complete rollout",
				func(ctx context.Context) ([]*hyperv1.ControlPlaneComponent, error) {
					list := &hyperv1.ControlPlaneComponentList{}
					err := testCtx.MgmtClient.List(ctx, list, crclient.InNamespace(testCtx.ControlPlaneNamespace))
					items := make([]*hyperv1.ControlPlaneComponent, len(list.Items))
					for i := range list.Items {
						items[i] = &list.Items[i]
					}
					return items, err
				},
				[]e2eutil.Predicate[[]*hyperv1.ControlPlaneComponent]{
					func(cpComponents []*hyperv1.ControlPlaneComponent) (done bool, reasons string, err error) {
						return len(cpComponents) > 10, "expecting more than 10 control plane components", nil
					},
				},
				[]e2eutil.Predicate[*hyperv1.ControlPlaneComponent]{
					e2eutil.ConditionPredicate[*hyperv1.ControlPlaneComponent](e2eutil.Condition{
						Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
						Status: metav1.ConditionTrue,
					}),
					func(cpComponent *hyperv1.ControlPlaneComponent) (done bool, reasons string, err error) {
						if startingVersion != "" && cpComponent.Status.Version == startingVersion {
							return false, fmt.Sprintf("component %s is still on version %s", cpComponent.Name, cpComponent.Status.Version), nil
						}
						return true, fmt.Sprintf("component %s has version: %s", cpComponent.Name, cpComponent.Status.Version), nil
					},
				},
				e2eutil.WithTimeout(30*time.Minute),
				e2eutil.WithInterval(10*time.Second),
			)

			// Step 2: Wait for controlPlaneVersion to complete rollout (4.22+).
			// Inlined from e2eutil.WaitForControlPlaneRollout / isControlPlaneVersionCompleted
			// because the v1 wrapper takes *testing.T.
			By("Waiting for control plane version to complete rollout")
			e2eutil.GinkgoAtLeast(e2eutil.Version422)
			e2eutil.EventuallyObject(GinkgoTB(), ctx, "control plane version to complete rollout",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					hc := &hyperv1.HostedCluster{}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
					return hc, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
						if hc.Status.ControlPlaneVersion.Desired.Image == "" {
							return false, "HostedCluster has no controlPlaneVersion status", nil
						}
						if len(hc.Status.ControlPlaneVersion.History) == 0 {
							return false, "HostedCluster controlPlaneVersion has no history", nil
						}
						entry := hc.Status.ControlPlaneVersion.History[0]
						if entry.Image != hc.Status.ControlPlaneVersion.Desired.Image {
							return false, fmt.Sprintf("controlPlaneVersion desired image %s doesn't match most recent image in history %s",
								hc.Status.ControlPlaneVersion.Desired.Image, entry.Image), nil
						}
						if entry.State != configv1.CompletedUpdate {
							return false, fmt.Sprintf("controlPlaneVersion state is %s, waiting for Completed", entry.State), nil
						}
						return true, "controlPlaneVersion reached Completed", nil
					},
				},
				e2eutil.WithTimeout(30*time.Minute),
				e2eutil.WithInterval(10*time.Second),
			)

			// Step 3: Wait for the data plane (CVO) rollout to complete.
			// Inlined from e2eutil.WaitForDataPlaneRollout because the v1 wrapper
			// takes *testing.T.
			By("Waiting for data plane (CVO) rollout to complete")
			e2eutil.EventuallyObject(GinkgoTB(), ctx, "data plane to complete rollout",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					hc := &hyperv1.HostedCluster{}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
					return hc, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.HostedClusterAvailable),
						Status: metav1.ConditionTrue,
					}),
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.HostedClusterProgressing),
						Status: metav1.ConditionFalse,
					}),
					func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
						if len(ptr.Deref(hc.Status.Version, hyperv1.ClusterVersionStatus{}).History) == 0 {
							return false, "HostedCluster has no version history", nil
						}
						if lastVersionCompletionTime != nil &&
							hc.Status.Version.History[0].CompletionTime != nil &&
							lastVersionCompletionTime.Equal(hc.Status.Version.History[0].CompletionTime) {
							return false, "HostedCluster version history has not been updated yet", nil
						}
						if wanted, got := hc.Status.Version.Desired.Image, hc.Status.Version.History[0].Image; wanted != got {
							return false, fmt.Sprintf("desired image %s doesn't match most recent image in history %s", wanted, got), nil
						}
						if wanted, got := configv1.CompletedUpdate, hc.Status.Version.History[0].State; wanted != got {
							return false, fmt.Sprintf("wanted most recent version history to have state %s, has state %s", wanted, got), nil
						}
						return true, "cluster rolled out", nil
					},
				},
				e2eutil.WithTimeout(30*time.Minute),
			)

			// TODO: Add post-upgrade validation checks once the e2eutil functions are
			// refactored to accept testing.TB instead of *testing.T. The following
			// checks are performed by the v1 test but cannot be called from Ginkgo:
			//   - e2eutil.EnsureFeatureGateStatus
			//   - e2eutil.EnsureNodeCountMatchesNodePoolReplicas
			//   - e2eutil.EnsureNoCrashingPods
			//   - e2eutil.EnsureMachineDeploymentGeneration
		})
	})
}

// RegisterControlPlaneUpgradeTests registers all control plane upgrade tests.
func RegisterControlPlaneUpgradeTests(getTestCtx internal.TestContextGetter) {
	ControlPlaneUpgradeTest(getTestCtx)
}

var _ = Describe("Control Plane Upgrade", Label("control-plane-upgrade"), func() {
	var (
		testCtx *internal.TestContext
	)

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		if err := testCtx.ValidateControlPlaneNamespace(); err != nil {
			AbortSuite(err.Error())
		}
	})

	RegisterControlPlaneUpgradeTests(func() *internal.TestContext { return testCtx })
})
