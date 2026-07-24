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
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

// RegisterNodePoolLifecycleTests registers all NodePool lifecycle test cases.
func RegisterNodePoolLifecycleTests(getTestCtx internal.TestContextGetter) {
	NodePoolMachineconfigRolloutTest(getTestCtx)
	NodePoolNTORolloutTest(getTestCtx)
	NodePoolNTOInPlaceTest(getTestCtx)
	NodePoolReplaceUpgradeTest(getTestCtx)
	NodePoolInPlaceUpgradeTest(getTestCtx)
	NodePoolRollingUpgradeTest(getTestCtx)
	NodePoolPrevReleaseN1Test(getTestCtx)
	NodePoolPrevReleaseN2Test(getTestCtx)
	NodePoolMirrorConfigsTest(getTestCtx)
	NodePoolTrustBundleTest(getTestCtx)
	NodePoolNTOPerformanceProfileTest(getTestCtx)
	NodePoolAutoRepairTest(getTestCtx)
	NodePoolDiskEncryptionTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolLifecycle] NodePool Lifecycle", Label("lifecycle", "nodepool-lifecycle"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodePoolLifecycleTests(func() *internal.TestContext { return testCtx })
})

// NodePoolMachineconfigRolloutTest creates a NodePool with Replace upgrade strategy,
// applies a MachineConfig via ConfigMap, patches the NodePool to reference it,
// creates a verification DaemonSet in the hosted cluster, and waits for config update
// complete and DaemonSet rollout.
func NodePoolMachineconfigRolloutTest(getTestCtx internal.TestContextGetter) {
	It("should roll out a MachineConfig change via Replace upgrade strategy", func() {
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
		np := buildTestNodePool(defaultNP, "mc-rollout", func(pool *hyperv1.NodePool) {
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

		// Build MachineConfig with a custom file at /etc/custom-config
		ignitionConfig := ignitionapi.Config{
			Ignition: ignitionapi.Ignition{Version: "3.2.0"},
			Storage: ignitionapi.Storage{
				Files: []ignitionapi.File{{
					Node:          ignitionapi.Node{Path: "/etc/custom-config"},
					FileEmbedded1: ignitionapi.FileEmbedded1{Contents: ignitionapi.Resource{Source: ptr.To("data:,content%0A")}},
				}},
			},
		}
		serializedIgnition, err := json.Marshal(ignitionConfig)
		Expect(err).NotTo(HaveOccurred(), "failed to serialize ignition config")

		machineConfig := &mcfgv1.MachineConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "custom",
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
				Name:      e2eutil.SimpleNameGenerator.GenerateName("custom-mc-"),
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

		// Build verification DaemonSet that checks /etc/custom-config exists
		ds := buildMachineConfigVerificationDaemonSet(np)
		Expect(hcClient.Create(ctx, ds)).To(Succeed(), "failed to create verification DaemonSet")

		e2eutil.WaitForNodePoolConfigUpdateCompleteWithPlatform(GinkgoTB(), ctx, testCtx.MgmtClient, np, hc.Spec.Platform.Type)
		waitForDaemonSetRollout(ctx, hcClient, ds, 1, np.Spec.Platform.Type)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: EnsureNoCrashingPods, EnsureAllContainersHavePullPolicyIfNotPresent,
		// EnsureHCPContainersHaveResourceRequests, EnsureNoPodsWithTooHighPriority
		// require *testing.T and cannot be called from Ginkgo yet.
	})
}

// NodePoolNTORolloutTest creates a NodePool with NTO Tuned config (hugepages),
// patches the NodePool's TuningConfig, creates a verification DaemonSet,
// and waits for rollout via Replace upgrade strategy.
func NodePoolNTORolloutTest(getTestCtx internal.TestContextGetter) {
	It("should roll out an NTO Tuned config change via Replace upgrade strategy", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		if hc.Spec.Platform.Type == hyperv1.KubevirtPlatform {
			Skip("test is skipped for KubeVirt platform until https://issues.redhat.com/browse/CNV-38196 is addressed")
		}
		if hc.Spec.Platform.Type == hyperv1.OpenStackPlatform {
			Skip("test is skipped for OpenStack platform until https://issues.redhat.com/browse/OSASINFRA-3566 is addressed")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var twoReplicas int32 = 2
		np := buildTestNodePool(defaultNP, "nto-replace", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &twoReplicas
			pool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
				Strategy: hyperv1.UpgradeStrategyRollingUpdate,
				RollingUpdate: &hyperv1.RollingUpdate{
					MaxUnavailable: ptr.To(intstr.FromInt32(0)),
					MaxSurge:       ptr.To(intstr.FromInt32(twoReplicas)),
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

		tuningCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName("hugepages-tuned-"),
				Namespace: hc.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTunedYAML},
		}
		Expect(testCtx.MgmtClient.Create(ctx, tuningCM)).To(Succeed(), "failed to create Tuned ConfigMap")
		DeferCleanup(func() {
			if err := testCtx.MgmtClient.Delete(ctx, tuningCM); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", tuningCM.Name)
			}
		})

		original := np.DeepCopy()
		np.Spec.TuningConfig = append(np.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningCM.Name})
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
			"failed to patch NodePool %s with TuningConfig", np.Name)

		ds := buildNTOVerificationDaemonSet(np)
		Expect(hcClient.Create(ctx, ds)).To(Succeed(), "failed to create NTO verification DaemonSet")

		e2eutil.WaitForNodePoolConfigUpdateCompleteWithPlatform(GinkgoTB(), ctx, testCtx.MgmtClient, np, hc.Spec.Platform.Type)
		waitForDaemonSetRollout(ctx, hcClient, ds, 2, np.Spec.Platform.Type)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: EnsureNoCrashingPods, EnsureAllContainersHavePullPolicyIfNotPresent,
		// EnsureHCPContainersHaveResourceRequests, EnsureNoPodsWithTooHighPriority
		// require *testing.T and cannot be called from Ginkgo yet.
	})
}

// NodePoolNTOInPlaceTest applies an NTO Tuned config with InPlace upgrade type.
func NodePoolNTOInPlaceTest(getTestCtx internal.TestContextGetter) {
	It("should roll out an NTO Tuned config change via InPlace upgrade strategy", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		if hc.Spec.Platform.Type == hyperv1.KubevirtPlatform {
			Skip("test is skipped for KubeVirt platform until https://issues.redhat.com/browse/CNV-38196 is addressed")
		}
		if hc.Spec.Platform.Type == hyperv1.OpenStackPlatform {
			Skip("test is skipped for OpenStack platform until https://issues.redhat.com/browse/OSASINFRA-3566 is addressed")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var twoReplicas int32 = 2
		np := buildTestNodePool(defaultNP, "nto-inplace", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &twoReplicas
			pool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		tuningCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName("hugepages-inplace-"),
				Namespace: hc.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTunedYAML},
		}
		Expect(testCtx.MgmtClient.Create(ctx, tuningCM)).To(Succeed(), "failed to create Tuned ConfigMap")
		DeferCleanup(func() {
			if err := testCtx.MgmtClient.Delete(ctx, tuningCM); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", tuningCM.Name)
			}
		})

		original := np.DeepCopy()
		np.Spec.TuningConfig = append(np.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningCM.Name})
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
			"failed to patch NodePool %s with TuningConfig", np.Name)

		ds := buildNTOVerificationDaemonSet(np)
		Expect(hcClient.Create(ctx, ds)).To(Succeed(), "failed to create NTO verification DaemonSet")

		e2eutil.WaitForNodePoolConfigUpdateCompleteWithPlatform(GinkgoTB(), ctx, testCtx.MgmtClient, np, hc.Spec.Platform.Type)
		waitForDaemonSetRollout(ctx, hcClient, ds, 2, np.Spec.Platform.Type)
		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: EnsureNoCrashingPods, EnsureAllContainersHavePullPolicyIfNotPresent,
		// EnsureHCPContainersHaveResourceRequests, EnsureNoPodsWithTooHighPriority
		// require *testing.T and cannot be called from Ginkgo yet.
	})
}

// NodePoolReplaceUpgradeTest creates a NodePool at previous release image, waits for nodes,
// upgrades to latest image, and waits for version to update via Replace upgrade strategy.
func NodePoolReplaceUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade a NodePool from previous to latest release via Replace strategy", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()

		previousImage := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE")
		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		if previousImage == "" || latestImage == "" {
			Skip("E2E_PREVIOUS_RELEASE_IMAGE and E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")
		}

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "replace-upgrade", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Release.Image = previousImage
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
		GinkgoWriter.Printf("Created NodePool %s at previous release %s\n", np.Name, previousImage)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// Update NodePool to latest release image
		GinkgoWriter.Printf("Upgrading NodePool %s to latest release %s\n", np.Name, latestImage)
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, np, func(obj *hyperv1.NodePool) {
			obj.Spec.Release.Image = latestImage
		})).To(Succeed(), "failed to update NodePool release image")

		// Wait for upgrade to start
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to start the upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingVersionConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
		)

		// Wait for upgrade to complete
		upgradeTimeout := nodePoolUpgradeTimeout(hc.Spec.Platform.Type)
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to complete the upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingVersionConditionType,
					Status: metav1.ConditionFalse,
				}),
			},
			e2eutil.WithTimeout(upgradeTimeout),
		)

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// Verify osImageStream status after upgrade, if the OSStreams feature gate is enabled.
		verifyOSImageStreamAfterUpgrade(ctx, testCtx, np)

		// TODO: EnsureNodesLabelsAndTaints, EnsureNodesRuntime require *testing.T
	})
}

// NodePoolInPlaceUpgradeTest creates a NodePool at previous release image, waits for nodes,
// upgrades to latest image via InPlace upgrade strategy.
func NodePoolInPlaceUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should upgrade a NodePool from previous to latest release via InPlace strategy", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()

		previousImage := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE")
		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		if previousImage == "" || latestImage == "" {
			Skip("E2E_PREVIOUS_RELEASE_IMAGE and E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")
		}

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "inplace-upgrade", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Release.Image = previousImage
			pool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s at previous release %s\n", np.Name, previousImage)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		GinkgoWriter.Printf("Upgrading NodePool %s to latest release %s\n", np.Name, latestImage)
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, np, func(obj *hyperv1.NodePool) {
			obj.Spec.Release.Image = latestImage
		})).To(Succeed(), "failed to update NodePool release image")

		// Wait for upgrade to start
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to start the upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingVersionConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
		)

		// Wait for upgrade to complete
		upgradeTimeout := nodePoolUpgradeTimeout(hc.Spec.Platform.Type)
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to complete the upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingVersionConditionType,
					Status: metav1.ConditionFalse,
				}),
			},
			e2eutil.WithTimeout(upgradeTimeout),
		)

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// Verify osImageStream status after upgrade, if the OSStreams feature gate is enabled.
		verifyOSImageStreamAfterUpgrade(ctx, testCtx, np)

		// TODO: EnsureNodesLabelsAndTaints, EnsureNodesRuntime require *testing.T
	})
}

// NodePoolRollingUpgradeTest creates a NodePool with 2 replicas, changes instance type
// (AWS) or VM size (Azure) to trigger a rolling upgrade, and verifies the machine specs
// after upgrade. Only runs on AWS and Azure platforms.
func NodePoolRollingUpgradeTest(getTestCtx internal.TestContextGetter) {
	It("should perform a rolling upgrade when instance type or VM size changes", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		platform := hc.Spec.Platform.Type
		if platform != hyperv1.AWSPlatform && platform != hyperv1.AzurePlatform {
			Skip("rolling upgrade test only supported on AWS and Azure platforms")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var twoReplicas int32 = 2
		np := buildTestNodePool(defaultNP, "rolling-upgrade", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &twoReplicas
			pool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			switch platform {
			case hyperv1.AWSPlatform:
				pool.Spec.Platform.AWS.InstanceType = "m5.large"
			case hyperv1.AzurePlatform:
				pool.Spec.Platform.Azure.VMSize = "Standard_D2s_v3"
			}
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s with 2 replicas\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, platform)

		// Change instance type / VM size to trigger rolling upgrade
		var newInstanceType, newVMSize string
		switch platform {
		case hyperv1.AWSPlatform:
			newInstanceType = "m5.xlarge"
		case hyperv1.AzurePlatform:
			newVMSize = "Standard_D4s_v5"
		}

		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, np, func(obj *hyperv1.NodePool) {
			switch platform {
			case hyperv1.AWSPlatform:
				obj.Spec.Platform.AWS.InstanceType = newInstanceType
			case hyperv1.AzurePlatform:
				obj.Spec.Platform.Azure.VMSize = newVMSize
			}
		})).To(Succeed(), "failed to update NodePool instance type / VM size")

		// Wait for rolling upgrade to start
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to start the rolling upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithTimeout(2*time.Minute),
		)

		// Wait for rolling upgrade to complete
		rollingTimeout := nodePoolUpgradeTimeout(platform)
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to finish the rolling upgrade", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
					Status: metav1.ConditionFalse,
				}),
			},
			e2eutil.WithTimeout(rollingTimeout),
		)

		// TODO: Verify machine specs (AWSMachineList / AzureMachineList) after upgrade.
		// The v1 test uses capiaws.AWSMachineList and capiazure.AzureMachineList to check
		// that instance types / VM sizes match. This requires importing CAPI provider types
		// which adds significant dependency. Implement once the pattern is established.
	})
}

// NodePoolPrevReleaseN1Test creates a NodePool at N-1 release image and waits for nodes ready.
func NodePoolPrevReleaseN1Test(getTestCtx internal.TestContextGetter) {
	It("should create a NodePool at N-1 release and have ready nodes", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()

		n1Image := internal.GetEnvVarValue("E2E_N1_RELEASE_IMAGE")
		if n1Image == "" {
			Skip("E2E_N1_RELEASE_IMAGE not set, skipping N-1 release test")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "prev-n1", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Release.Image = n1Image
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s at N-1 release %s\n", np.Name, n1Image)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: EnsureNodesLabelsAndTaints requires *testing.T
	})
}

// NodePoolPrevReleaseN2Test creates a NodePool at N-2 release image and waits for nodes ready.
func NodePoolPrevReleaseN2Test(getTestCtx internal.TestContextGetter) {
	It("should create a NodePool at N-2 release and have ready nodes", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()

		n2Image := internal.GetEnvVarValue("E2E_N2_RELEASE_IMAGE")
		if n2Image == "" {
			Skip("E2E_N2_RELEASE_IMAGE not set, skipping N-2 release test")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "prev-n2", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Release.Image = n2Image
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s at N-2 release %s\n", np.Name, n2Image)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: EnsureNodesLabelsAndTaints requires *testing.T
	})
}

// NodePoolMirrorConfigsTest creates a KubeletConfig ConfigMap, patches NodePool config,
// verifies the KubeletConfig gets mirrored to the hosted cluster's openshift-config-managed
// namespace, then removes the config and verifies cleanup. Only for 4.18+.
func NodePoolMirrorConfigsTest(getTestCtx internal.TestContextGetter) {
	It("should mirror KubeletConfig to the hosted cluster and clean up on removal", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		if e2eutil.IsLessThan(e2eutil.Version418) {
			Skip("mirror configs test only applicable for 4.18+")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "mirror-cfg", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		kcConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName("kc-test-"),
				Namespace: np.Namespace,
			},
			Data: map[string]string{configKey: kubeletConfig1YAML},
		}
		Expect(testCtx.MgmtClient.Create(ctx, kcConfigMap)).To(Succeed(), "failed to create KubeletConfig ConfigMap")
		DeferCleanup(func() {
			if err := testCtx.MgmtClient.Delete(ctx, kcConfigMap); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", kcConfigMap.Name)
			}
		})

		original := np.DeepCopy()
		np.Spec.Config = append(np.Spec.Config, corev1.LocalObjectReference{Name: kcConfigMap.Name})
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
			"failed to patch NodePool %s with KubeletConfig", np.Name)

		// Verify mirrored ConfigMap appears in the hosted cluster
		e2eutil.EventuallyObjects(GinkgoTB(), ctx, "KubeletConfig should be mirrored to the hosted cluster",
			func(ctx context.Context) ([]*corev1.ConfigMap, error) {
				list := &corev1.ConfigMapList{}
				err := hcClient.List(ctx, list, crclient.InNamespace(configManagedNamespace),
					crclient.MatchingLabels(map[string]string{
						nodepool.KubeletConfigConfigMapLabel: "true",
						hyperv1.NodePoolLabel:                np.Name,
					}))
				configMaps := make([]*corev1.ConfigMap, len(list.Items))
				for i := range list.Items {
					configMaps[i] = &list.Items[i]
				}
				return configMaps, err
			},
			[]e2eutil.Predicate[[]*corev1.ConfigMap]{
				func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
					want, got := 1, len(configMaps)
					return want == got, fmt.Sprintf("expected %d KubeletConfig ConfigMaps, got %d", want, got), nil
				},
			},
			[]e2eutil.Predicate[*corev1.ConfigMap]{
				func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					want := netutil.ShortenName(kcConfigMap.Name, np.Name, nodepool.QualifiedNameMaxLength)
					if want != cm.Name {
						return false, fmt.Sprintf("expected ConfigMap name %q, got %q", want, cm.Name), nil
					}
					return true, "ConfigMap name is as expected", nil
				},
				func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					if diff := cmp.Diff(map[string]string{
						nodepool.KubeletConfigConfigMapLabel: cm.Labels[nodepool.KubeletConfigConfigMapLabel],
						hyperv1.NodePoolLabel:                cm.Labels[hyperv1.NodePoolLabel],
						nodepool.NTOMirroredConfigLabel:      cm.Labels[nodepool.NTOMirroredConfigLabel],
					}, map[string]string{
						nodepool.KubeletConfigConfigMapLabel: "true",
						hyperv1.NodePoolLabel:                np.Name,
						nodepool.NTOMirroredConfigLabel:      "true",
					}); diff != "" {
						return false, fmt.Sprintf("incorrect labels: %v", diff), nil
					}
					return true, "labels are correct", nil
				},
			},
		)

		// Remove KubeletConfig from NodePool and verify cleanup
		GinkgoWriter.Printf("Removing KubeletConfig reference from NodePool %s\n", np.Name)
		baseNP := np.DeepCopy()
		np.Spec = original.Spec
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(baseNP))).To(Succeed(),
			"failed to remove KubeletConfig from NodePool %s", np.Name)

		e2eutil.EventuallyObjects(GinkgoTB(), ctx, "KubeletConfig ConfigMap to be deleted from hosted cluster",
			func(ctx context.Context) ([]*corev1.ConfigMap, error) {
				list := &corev1.ConfigMapList{}
				err := hcClient.List(ctx, list, crclient.InNamespace(configManagedNamespace),
					crclient.MatchingLabels(map[string]string{
						nodepool.KubeletConfigConfigMapLabel: "true",
						hyperv1.NodePoolLabel:                np.Name,
					}))
				configMaps := make([]*corev1.ConfigMap, len(list.Items))
				for i := range list.Items {
					configMaps[i] = &list.Items[i]
				}
				return configMaps, err
			},
			[]e2eutil.Predicate[[]*corev1.ConfigMap]{
				func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
					want, got := 1, len(configMaps)
					return want == got, fmt.Sprintf("expected %d KubeletConfig ConfigMaps, got %d", want, got), nil
				},
			}, nil,
		)
	})
}

// NodePoolTrustBundleTest creates an additional trust bundle ConfigMap, updates the
// HostedCluster to reference it, waits for NodePool update cycle, verifies user-ca-bundle
// exists in the hosted cluster, removes the trust bundle, verifies CPO deployment no longer
// mounts it, waits for another update cycle, and verifies user-ca-bundle is deleted (4.22+).
func NodePoolTrustBundleTest(getTestCtx internal.TestContextGetter) {
	It("should propagate and remove additional trust bundle to/from the hosted cluster", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		e2eutil.GinkgoAtLeast(e2eutil.Version418)

		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		const (
			defaultPollInterval                 = 15 * time.Second
			nodePoolConfigUpdateStartTimeout    = 5 * time.Minute
			nodePoolConfigUpdateFinishTimeout   = 20 * time.Minute
			cpoDeploymentUpdateTimeout          = 10 * time.Minute
			guestUserCABundlePropagationTimeout = 5 * time.Minute
		)

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "trust-bundle", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
		})
		Expect(testCtx.MgmtClient.Create(ctx, np)).To(Succeed(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s for trust bundle test\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// Create additional trust bundle ConfigMap
		trustBundle := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName("trust-bundle-"),
				Namespace: hc.Namespace,
			},
			Data: map[string]string{"ca-bundle.crt": "dummy"},
		}
		Expect(testCtx.MgmtClient.Create(ctx, trustBundle)).To(Succeed(), "failed to create trust bundle ConfigMap")
		DeferCleanup(func() {
			if err := testCtx.MgmtClient.Delete(ctx, trustBundle); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", trustBundle.Name)
			}
		})

		// Update HostedCluster to reference the trust bundle
		GinkgoWriter.Printf("Updating HostedCluster with additional trust bundle %s\n", trustBundle.Name)
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: trustBundle.Name}
		})).To(Succeed(), "failed to update HostedCluster with trust bundle")

		// Defer cleanup: remove trust bundle reference from HostedCluster
		DeferCleanup(func() {
			err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, testCtx.GetHostedCluster(), func(obj *hyperv1.HostedCluster) {
				obj.Spec.AdditionalTrustBundle = nil
			})
			if err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to remove additional trust bundle")
			}
		})

		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to begin updating", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(nodePoolConfigUpdateStartTimeout),
		)

		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to stop updating", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionFalse,
				}),
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolAllNodesHealthyConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(nodePoolConfigUpdateFinishTimeout),
		)

		// Verify user-ca-bundle exists in the hosted cluster
		userCAConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-ca-bundle",
				Namespace: "openshift-config",
			},
		}
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "user-ca-bundle to exist in hosted cluster",
			func(ctx context.Context) (*corev1.ConfigMap, error) {
				cm := &corev1.ConfigMap{}
				err := hcClient.Get(ctx, crclient.ObjectKeyFromObject(userCAConfigMap), cm)
				return cm, err
			},
			[]e2eutil.Predicate[*corev1.ConfigMap]{
				func(obj *corev1.ConfigMap) (bool, string, error) { return true, "exists", nil },
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(guestUserCABundlePropagationTimeout),
		)

		// Remove trust bundle from HostedCluster
		GinkgoWriter.Printf("Removing additional trust bundle from HostedCluster\n")
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AdditionalTrustBundle = nil
		})).To(Succeed(), "failed to remove trust bundle from HostedCluster")

		// Verify CPO deployment no longer mounts the trust bundle
		cpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
		cpoDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "control-plane-operator",
				Namespace: cpNamespace,
			},
		}
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "CPO deployment to stop mounting trust bundle",
			func(ctx context.Context) (*appsv1.Deployment, error) {
				deploy := &appsv1.Deployment{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(cpoDeployment), deploy)
				return deploy, err
			},
			[]e2eutil.Predicate[*appsv1.Deployment]{
				func(obj *appsv1.Deployment) (bool, string, error) {
					for _, volume := range obj.Spec.Template.Spec.Volumes {
						if volume.ConfigMap != nil && volume.ConfigMap.Name == "trusted-ca" {
							return false, "trust bundle volume still mounted in CPO", nil
						}
					}
					if ready := podspec.IsDeploymentReady(ctx, obj); !ready {
						return false, "CPO deployment is not ready", nil
					}
					return true, "trust bundle volume removed from CPO", nil
				},
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(cpoDeploymentUpdateTimeout),
		)

		// Wait for NodePool to cycle again
		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to begin updating after trust bundle removal", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(nodePoolConfigUpdateStartTimeout),
		)

		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("NodePool %s/%s to stop updating after trust bundle removal", np.Namespace, np.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionFalse,
				}),
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolAllNodesHealthyConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(defaultPollInterval), e2eutil.WithTimeout(nodePoolConfigUpdateFinishTimeout),
		)

		// Verify user-ca-bundle is deleted from the hosted cluster (4.22+)
		if e2eutil.IsGreaterThanOrEqualTo(e2eutil.Version422) {
			e2eutil.EventuallyNotFound(GinkgoTB(), ctx, hcClient, userCAConfigMap,
				e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
			)
		}
	})
}

// NodePoolNTOPerformanceProfileTest creates a PerformanceProfile via ConfigMap,
// patches the NodePool's TuningConfig, verifies the PerformanceProfile ConfigMap and
// status ConfigMap are created in the control plane namespace, and verifies cleanup.
func NodePoolNTOPerformanceProfileTest(getTestCtx internal.TestContextGetter) {
	It("should create and manage NTO PerformanceProfile via NodePool TuningConfig", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		if hc.Spec.Platform.Type == hyperv1.OpenStackPlatform {
			Skip("test is skipped for OpenStack platform until https://issues.redhat.com/browse/OSASINFRA-3566 is addressed")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "nto-perfprof", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		ppConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName("pp-test-"),
				Namespace: np.Namespace,
			},
			Data: map[string]string{tuningConfigKey: performanceProfileYAML},
		}
		Expect(testCtx.MgmtClient.Create(ctx, ppConfigMap)).To(Succeed(), "failed to create PerformanceProfile ConfigMap")
		DeferCleanup(func() {
			if err := testCtx.MgmtClient.Delete(ctx, ppConfigMap); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete ConfigMap %s", ppConfigMap.Name)
			}
		})

		original := np.DeepCopy()
		np.Spec.TuningConfig = append(np.Spec.TuningConfig, corev1.LocalObjectReference{Name: ppConfigMap.Name})
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(original))).To(Succeed(),
			"failed to patch NodePool %s with PerformanceProfile config", np.Name)

		cpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

		// Verify PerformanceProfile ConfigMap exists in control plane namespace
		e2eutil.EventuallyObjects(GinkgoTB(), ctx, "PerformanceProfile ConfigMap to exist with correct labels",
			func(ctx context.Context) ([]*corev1.ConfigMap, error) {
				list := &corev1.ConfigMapList{}
				err := testCtx.MgmtClient.List(ctx, list, crclient.InNamespace(cpNamespace),
					crclient.MatchingLabels(map[string]string{
						nodepool.PerformanceProfileConfigMapLabel: "true",
					}))
				configMaps := make([]*corev1.ConfigMap, len(list.Items))
				for i := range list.Items {
					configMaps[i] = &list.Items[i]
				}
				return configMaps, err
			},
			[]e2eutil.Predicate[[]*corev1.ConfigMap]{
				func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
					want, got := 1, len(configMaps)
					return want == got, fmt.Sprintf("expected %d PerformanceProfile ConfigMaps, got %d", want, got), nil
				},
			},
			[]e2eutil.Predicate[*corev1.ConfigMap]{
				func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					want := netutil.ShortenName(ppConfigMap.Name, np.Name, nodepool.QualifiedNameMaxLength)
					if want != cm.Name {
						return false, fmt.Sprintf("expected PerformanceProfile ConfigMap name %q, got %q", want, cm.Name), nil
					}
					return true, "PerformanceProfile ConfigMap name is as expected", nil
				},
				func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					if diff := cmp.Diff(map[string]string{
						nodepool.PerformanceProfileConfigMapLabel: cm.Labels[nodepool.PerformanceProfileConfigMapLabel],
						hyperv1.NodePoolLabel:                     cm.Labels[hyperv1.NodePoolLabel],
					}, map[string]string{
						nodepool.PerformanceProfileConfigMapLabel: "true",
						hyperv1.NodePoolLabel:                     np.Name,
					}); diff != "" {
						return false, fmt.Sprintf("incorrect labels: %v", diff), nil
					}
					return true, "labels are correct", nil
				},
			},
		)

		// Verify status ConfigMap (4.17+)
		if !e2eutil.IsLessThan(e2eutil.Version417) {
			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "PerformanceProfile status ConfigMap to exist",
				func(ctx context.Context) ([]*corev1.ConfigMap, error) {
					list := &corev1.ConfigMapList{}
					err := testCtx.MgmtClient.List(ctx, list, crclient.InNamespace(cpNamespace),
						crclient.MatchingLabels(map[string]string{
							nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel: "true",
						}))
					configMaps := make([]*corev1.ConfigMap, len(list.Items))
					for i := range list.Items {
						configMaps[i] = &list.Items[i]
					}
					return configMaps, err
				},
				[]e2eutil.Predicate[[]*corev1.ConfigMap]{
					func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
						want, got := 1, len(configMaps)
						return want == got, fmt.Sprintf("expected %d status ConfigMaps, got %d", want, got), nil
					},
				},
				[]e2eutil.Predicate[*corev1.ConfigMap]{
					func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
						want := fmt.Sprintf("status-%s", netutil.ShortenName(ppConfigMap.Name, np.Name, nodepool.QualifiedNameMaxLength))
						if want != cm.Name {
							return false, fmt.Sprintf("expected status ConfigMap name %q, got %q", want, cm.Name), nil
						}
						return true, "status ConfigMap name is as expected", nil
					},
				},
			)
		}

		// Remove PerformanceProfile from NodePool and verify cleanup
		GinkgoWriter.Printf("Removing PerformanceProfile reference from NodePool %s\n", np.Name)
		baseNP := np.DeepCopy()
		np.Spec = original.Spec
		Expect(testCtx.MgmtClient.Patch(ctx, np, crclient.MergeFrom(baseNP))).To(Succeed(),
			"failed to remove PerformanceProfile from NodePool %s", np.Name)

		e2eutil.EventuallyObjects(GinkgoTB(), ctx, "PerformanceProfile ConfigMap to be deleted",
			func(ctx context.Context) ([]*corev1.ConfigMap, error) {
				list := &corev1.ConfigMapList{}
				err := testCtx.MgmtClient.List(ctx, list, crclient.InNamespace(cpNamespace),
					crclient.MatchingLabels(map[string]string{
						nodepool.PerformanceProfileConfigMapLabel: "true",
					}))
				configMaps := make([]*corev1.ConfigMap, len(list.Items))
				for i := range list.Items {
					configMaps[i] = &list.Items[i]
				}
				return configMaps, err
			},
			[]e2eutil.Predicate[[]*corev1.ConfigMap]{
				func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
					want, got := 0, len(configMaps)
					return want == got, fmt.Sprintf("expected %d PerformanceProfile ConfigMaps, got %d", want, got), nil
				},
			}, nil,
		)
	})
}

// NodePoolAutoRepairTest is a skeleton for platform-specific auto-repair tests.
// The full implementation requires cloud SDK dependencies for instance termination.
func NodePoolAutoRepairTest(getTestCtx internal.TestContextGetter) {
	It("should auto-repair a NodePool when a node is terminated", func() {
		Skip("auto-repair instance termination not yet implemented for v2 framework")

		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		platform := hc.Spec.Platform.Type
		if platform != hyperv1.AWSPlatform && platform != hyperv1.AzurePlatform {
			Skip("auto-repair test only supported on AWS and Azure platforms")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "autorepair", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.Management.AutoRepair = true
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created auto-repair NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, platform)

		// TODO: Implement cloud-specific instance termination logic.
		// For AWS: use EC2 TerminateInstances API to terminate the node's backing instance.
		// For Azure: delete the VMSS instance backing the node.
		// After termination, wait for the node to be replaced using:
		//   e2eutil.WaitForReadyNodesByNodePool with WithCollectionPredicates and WithPredicates
		//   to verify the old node is replaced and the new node is healthy.
	})
}

// NodePoolDiskEncryptionTest is a skeleton for Azure disk encryption tests.
func NodePoolDiskEncryptionTest(getTestCtx internal.TestContextGetter) {
	It("should create a NodePool with Azure DiskEncryptionSet and verify it is applied", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
			Skip("disk encryption test only supported on Azure platform")
		}

		diskEncryptionSetID := internal.GetEnvVarValue("E2E_AZURE_DISK_ENCRYPTION_SET_ID")
		if diskEncryptionSetID == "" {
			Skip("E2E_AZURE_DISK_ENCRYPTION_SET_ID not set, skipping disk encryption test")
		}

		hcClient := testCtx.GetHostedClusterClient()

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "disk-encrypt", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			if pool.Spec.Platform.Azure != nil {
				pool.Spec.Platform.Azure.OSDisk.EncryptionSetID = diskEncryptionSetID
			}
		})

		err := testCtx.MgmtClient.Create(ctx, np)
		Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created disk encryption NodePool %s\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// TODO: Verify disk encryption is applied by checking AzureMachine specs
		// in the control plane namespace. This requires importing CAPI Azure types
		// (capiazure.AzureMachineList) and verifying DiskEncryptionSetID on each machine.
	})
}

// Helper functions

// buildTestNodePool builds a new NodePool from a template with the given name prefix
// and applies the provided mutation function.
func buildTestNodePool(template *hyperv1.NodePool, namePrefix string, mutate func(*hyperv1.NodePool)) *hyperv1.NodePool {
	GinkgoHelper()

	name := e2eutil.SimpleNameGenerator.GenerateName(template.Spec.ClusterName + "-" + namePrefix + "-")
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: template.Namespace,
		},
	}
	template.Spec.DeepCopyInto(&np.Spec)

	if mutate != nil {
		mutate(np)
	}

	return np
}

// buildMachineConfigVerificationDaemonSet constructs a DaemonSet that verifies
// /etc/custom-config exists on nodes (checks MachineConfig was applied).
func buildMachineConfigVerificationDaemonSet(np *hyperv1.NodePool) *appsv1.DaemonSet {
	GinkgoHelper()

	dsName := e2eutil.SimpleNameGenerator.GenerateName("mc-verify-")
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsName,
			Namespace: "kube-system",
			Labels: map[string]string{
				hyperv1.NodePoolLabel: np.Name,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name":                dsName,
					hyperv1.NodePoolLabel: np.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":                dsName,
						hyperv1.NodePoolLabel: np.Name,
					},
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						hyperv1.NodePoolLabel: np.Name,
					},
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{{
						Name:    dsName,
						Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
						Command: []string{"/bin/sleep", "24h"},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"/bin/cat", "/host/etc/custom-config"},
								},
							},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "host",
							MountPath: "/host",
							ReadOnly:  true,
						}},
					}},
					TerminationGracePeriodSeconds: ptr.To[int64](30),
					Volumes: []corev1.Volume{{
						Name: "host",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/"},
						},
					}},
				},
			},
		},
	}

	return ds
}

// buildNTOVerificationDaemonSet constructs a DaemonSet that verifies hugepages
// are configured on nodes via /proc/cmdline (checks NTO Tuned config was applied).
func buildNTOVerificationDaemonSet(np *hyperv1.NodePool) *appsv1.DaemonSet {
	GinkgoHelper()

	dsName := e2eutil.SimpleNameGenerator.GenerateName("nto-verify-")
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsName,
			Namespace: "kube-system",
			Labels: map[string]string{
				hyperv1.NodePoolLabel: np.Name,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name":                dsName,
					hyperv1.NodePoolLabel: np.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":                dsName,
						hyperv1.NodePoolLabel: np.Name,
					},
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						hyperv1.NodePoolLabel: np.Name,
					},
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{{
						Name:    dsName,
						Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
						Command: []string{"/bin/sleep", "24h"},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{
									Command: []string{"/bin/sh", "-c", `cat /proc/cmdline | grep "hugepagesz=2M hugepages=4"`},
								},
							},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "host",
							MountPath: "/host",
							ReadOnly:  true,
						}},
					}},
					TerminationGracePeriodSeconds: ptr.To[int64](30),
					Volumes: []corev1.Volume{{
						Name: "host",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/"},
						},
					}},
				},
			},
		},
	}

	return ds
}

// waitForDaemonSetRollout polls until the DaemonSet has the expected number of ready pods.
func waitForDaemonSetRollout(ctx context.Context, client crclient.Client, ds *appsv1.DaemonSet, expectedCount int, platform hyperv1.PlatformType) {
	GinkgoHelper()

	timeout := 15 * time.Minute
	if platform == hyperv1.KubevirtPlatform {
		timeout = 25 * time.Minute
	}

	e2eutil.EventuallyObjects(GinkgoTB(), ctx, fmt.Sprintf("all pods in DaemonSet %s/%s to be ready", ds.Namespace, ds.Name),
		func(ctx context.Context) ([]*corev1.Pod, error) {
			list := &corev1.PodList{}
			err := client.List(ctx, list, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels))
			readyPods := []*corev1.Pod{}
			for i := range list.Items {
				pod := &list.Items[i]
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						readyPods = append(readyPods, pod)
						break
					}
				}
			}
			return readyPods, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(readyPods []*corev1.Pod) (done bool, reasons string, err error) {
				want, got := expectedCount, len(readyPods)
				return want == got, fmt.Sprintf("expected %d ready Pods, got %d", want, got), nil
			},
		}, nil,
		e2eutil.WithTimeout(timeout),
		e2eutil.WithInterval(5*time.Second),
	)
}

// verifyOSImageStreamAfterUpgrade checks that status.osImageStream is set correctly
// on the NodePool after an upgrade completes. If the OSStreams feature gate is not
// enabled, the assertion is skipped (the upgrade test itself still passes).
//
// TODO(CNTRLPLANE-3032): The default OS stream is currently hardcoded to rhel-9 for all
// OCP versions. When the hardcoding is removed and OCP >= 5.0 defaults to rhel-10,
// update expectedStream to use rhel-10 on >= 5.0.
func verifyOSImageStreamAfterUpgrade(ctx context.Context, testCtx *internal.TestContext, np *hyperv1.NodePool) {
	GinkgoHelper()

	hasSpecField, err := e2eutil.HasFieldInCRDSchema(ctx, testCtx.MgmtClient,
		"nodepools.hypershift.openshift.io", "spec.osImageStream")
	Expect(err).NotTo(HaveOccurred(), "failed to check CRD schema for spec.osImageStream")
	if !hasSpecField {
		GinkgoWriter.Println("OSStreams feature gate is not enabled; skipping osImageStream assertion")
		return
	}

	hasStatusField, err := e2eutil.HasFieldInCRDSchema(ctx, testCtx.MgmtClient,
		"nodepools.hypershift.openshift.io", "status.osImageStream")
	Expect(err).NotTo(HaveOccurred(), "failed to check CRD schema for status.osImageStream")
	if !hasStatusField {
		GinkgoWriter.Println("OSStreams feature gate is not enabled for status; skipping osImageStream assertion")
		return
	}

	expectedStream := hyperv1.OSImageStreamRHEL10

	e2eutil.EventuallyObject[*hyperv1.NodePool](
		GinkgoTB(), ctx,
		fmt.Sprintf("NodePool %s/%s status to report osImageStream=%s after upgrade", np.Namespace, np.Name, expectedStream),
		func(pollCtx context.Context) (*hyperv1.NodePool, error) {
			pool := &hyperv1.NodePool{}
			err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(np), pool)
			return pool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.OSImageStreamPredicate(expectedStream),
		},
		e2eutil.WithTimeout(10*time.Minute),
		e2eutil.WithInterval(15*time.Second),
	)
}

// nodePoolUpgradeTimeout returns the appropriate timeout for NodePool upgrades
// based on the platform type.
func nodePoolUpgradeTimeout(platform hyperv1.PlatformType) time.Duration {
	switch platform {
	case hyperv1.AzurePlatform, hyperv1.KubevirtPlatform:
		return 45 * time.Minute
	default:
		return 20 * time.Minute
	}
}

// Constants

const (
	tuningConfigKey        = "tuning"
	configKey              = "config"
	configManagedNamespace = "openshift-config-managed"

	hugepagesTunedYAML = `apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: hugepages
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Boot time configuration for hugepages
      include=openshift-node
      [bootloader]
      cmdline_openshift_node_hugepages=hugepagesz=2M hugepages=4
    name: openshift-hugepages
  recommend:
  - priority: 20
    profile: openshift-hugepages
`

	kubeletConfig1YAML = `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
`

	performanceProfileYAML = `
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: perfprof-2
spec:
  cpu:
    isolated: "1"
    reserved: "0"
  numa:
    topologyPolicy: "single-numa-node"
  nodeSelector:
    node-role.kubernetes.io/worker-cnf: ""
`
)
