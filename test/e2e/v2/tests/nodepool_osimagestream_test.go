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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// nodePoolAnnotationCurrentConfig mirrors the annotation key from the NodePool controller.
	// Used to verify that setting the version-derived default stream does not change the config hash.
	nodePoolAnnotationCurrentConfig = "hypershift.openshift.io/nodePoolCurrentConfig"
)

// RegisterNodePoolOSImageStreamLifecycleTests registers lifecycle (state-mutating)
// NodePool OS image stream test cases.
func RegisterNodePoolOSImageStreamLifecycleTests(getTestCtx internal.TestContextGetter) {
	NodePoolOSImageStreamRHEL10RejectionTest(getTestCtx)
	NodePoolOSImageStreamRHEL10RuncRejectionTest(getTestCtx)
	NodePoolOSImageStreamExplicitDefaultNoRolloutTest(getTestCtx)
}

// RegisterNodePoolOSImageStreamStatusTests registers non-lifecycle (read-only)
// NodePool OS image stream test cases.
func RegisterNodePoolOSImageStreamStatusTests(getTestCtx internal.TestContextGetter) {
	NodePoolOSImageStreamDefaultStatusTest(getTestCtx)
}

// osImageStreamBeforeEach is the shared BeforeEach for all OSImageStream test suites.
// It initializes the test context and skips when the OSStreams feature gate is disabled.
func osImageStreamBeforeEach(testCtx **internal.TestContext) {
	*testCtx = internal.GetTestContext()
	Expect(*testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

	// TODO(sdminonne): remove this skip once status.osImageStream is populated on
	// GCP/GKE. Currently the CAPI Machine NodeInfo.OSImage string on GKE does not
	// match the RHCOS pattern the controller uses to infer the OS stream, so the
	// status field is never set and all OSImageStream tests time out.
	hc := (*testCtx).GetHostedCluster()
	if hc != nil && hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		Skip("OSImageStream tests are temporarily skipped on GCP platform")
	}

	hasOSStream, err := e2eutil.HasFieldInCRDSchema((*testCtx).Context, (*testCtx).MgmtClient,
		"nodepools.hypershift.openshift.io", "spec.osImageStream")
	Expect(err).NotTo(HaveOccurred(), "failed to check CRD schema for spec.osImageStream")
	if !hasOSStream {
		Skip("OSStreams feature gate is not enabled: spec.osImageStream field not present in NodePool CRD")
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolOSImageStream] NodePool OSImageStream Lifecycle", Label("lifecycle", "nodepool-osimagestream"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		osImageStreamBeforeEach(&testCtx)
	})

	RegisterNodePoolOSImageStreamLifecycleTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolOSImageStream] NodePool OSImageStream Status", Label("nodepool-osimagestream"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		osImageStreamBeforeEach(&testCtx)
	})

	RegisterNodePoolOSImageStreamStatusTests(func() *internal.TestContext { return testCtx })
})

// NodePoolOSImageStreamRHEL10RejectionTest verifies that setting osImageStream to
// rhel-10 on OCP < 5.0 causes the controller to set ValidMachineConfig=False with
// reason ValidationFailed. Uses 0 replicas since no nodes are needed.
// This test only applies to OCP < 5.0 because rhel-10 is valid on OCP >= 5.0.
func NodePoolOSImageStreamRHEL10RejectionTest(getTestCtx internal.TestContextGetter) {
	It("When osImageStream is set to rhel-10 on OCP < 5.0, it should set ValidMachineConfig to False", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedCluster()

		if !e2eutil.IsLessThan(e2eutil.Version50) {
			Skip("test only applies to OCP < 5.0; rhel-10 is valid on OCP >= 5.0")
		}

		hc := testCtx.GetHostedCluster()
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var zeroReplicas int32 = 0
		np := buildTestNodePool(defaultNP, "osstream-rhel10", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &zeroReplicas
			pool.Spec.OSImageStream = hyperv1.OSImageStreamReference{
				Name: hyperv1.OSImageStreamRHEL10,
			}
		})

		Expect(testCtx.MgmtClient.Create(ctx, np)).To(Succeed(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s with osImageStream=rhel-10\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			"NodePool to have ValidMachineConfig=False with ValidationFailed and version message",
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolValidMachineConfigConditionType,
					Status: metav1.ConditionFalse,
					Reason: hyperv1.NodePoolValidationFailedReason,
				}),
				conditionMessageContains(hyperv1.NodePoolValidMachineConfigConditionType, "requires OCP >= 5.0"),
			},
			e2eutil.WithTimeout(5*time.Minute),
			e2eutil.WithInterval(10*time.Second),
		)
	})
}

// NodePoolOSImageStreamRHEL10RuncRejectionTest verifies that setting osImageStream
// to rhel-10 with a ContainerRuntimeConfig that sets defaultRuntime to runc causes
// the controller to set ValidMachineConfig=False with reason ValidationFailed.
// RHEL 10 does not ship runc, so this combination is invalid.
// Uses 0 replicas since no nodes are needed.
// This test only applies to OCP >= 5.0 because on < 5.0 rhel-10 is rejected for
// version reasons before the runc check is reached.
func NodePoolOSImageStreamRHEL10RuncRejectionTest(getTestCtx internal.TestContextGetter) {
	It("When osImageStream is set to rhel-10 with runc ContainerRuntimeConfig, it should set ValidMachineConfig to False", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedCluster()

		if e2eutil.IsLessThan(e2eutil.Version50) {
			Skip("test only applies to OCP >= 5.0; on < 5.0 rhel-10 is rejected for version reasons")
		}

		hc := testCtx.GetHostedCluster()
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create a ConfigMap with a ContainerRuntimeConfig that sets defaultRuntime to runc.
		runcConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e2eutil.SimpleNameGenerator.GenerateName(hc.Name + "-runc-ctrcfg-"),
				Namespace: hc.Namespace,
			},
			Data: map[string]string{
				"config": `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: set-runc
spec:
  containerRuntimeConfig:
    defaultRuntime: runc
`,
			},
		}
		Expect(testCtx.MgmtClient.Create(ctx, runcConfigMap)).To(Succeed(),
			"failed to create runc ContainerRuntimeConfig ConfigMap %s", runcConfigMap.Name)
		GinkgoWriter.Printf("Created runc ContainerRuntimeConfig ConfigMap %s\n", runcConfigMap.Name)
		DeferCleanup(func() {
			err := testCtx.MgmtClient.Delete(ctx, runcConfigMap)
			if err != nil && !apierrors.IsNotFound(err) {
				GinkgoWriter.Printf("Warning: failed to delete ConfigMap %s: %v\n", runcConfigMap.Name, err)
			}
		})

		var zeroReplicas int32 = 0
		np := buildTestNodePool(defaultNP, "osstream-runc", func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &zeroReplicas
			pool.Spec.OSImageStream = hyperv1.OSImageStreamReference{
				Name: hyperv1.OSImageStreamRHEL10,
			}
			pool.Spec.Config = []corev1.LocalObjectReference{
				{Name: runcConfigMap.Name},
			}
		})

		Expect(testCtx.MgmtClient.Create(ctx, np)).To(Succeed(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s with osImageStream=rhel-10 and runc config\n", np.Name)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			"NodePool to have ValidMachineConfig=False with ValidationFailed and runc message",
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(np), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolValidMachineConfigConditionType,
					Status: metav1.ConditionFalse,
					Reason: hyperv1.NodePoolValidationFailedReason,
				}),
				conditionMessageContains(hyperv1.NodePoolValidMachineConfigConditionType, "incompatible with runc"),
			},
			e2eutil.WithTimeout(5*time.Minute),
			e2eutil.WithInterval(10*time.Second),
		)
	})
}

// NodePoolOSImageStreamDefaultStatusTest verifies that the existing default NodePool
// (no osImageStream set) reports rhel-9 in status.osImageStream.
// This is a non-lifecycle test: it reads existing state without mutation.
//
// TODO(CNTRLPLANE-3032): The default OS stream is currently hardcoded to rhel-9 for all
// OCP versions. When the hardcoding is removed and OCP >= 5.0 defaults to rhel-10,
// this test must be updated to expect rhel-10 on OCP >= 5.0:
//
//	expectedStream := hyperv1.OSImageStreamRHEL9
//	if e2eutil.IsGreaterThanOrEqualTo(e2eutil.Version50) {
//	    expectedStream = hyperv1.OSImageStreamRHEL10
//	}
func NodePoolOSImageStreamDefaultStatusTest(getTestCtx internal.TestContextGetter) {
	It("When no osImageStream is set, it should report rhel-9 in status", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedCluster()

		hc := testCtx.GetHostedCluster()
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// This test verifies default resolution — skip if the default NodePool
		// explicitly sets osImageStream (the test would verify explicit, not default).
		if defaultNP.Spec.OSImageStream.Name != "" {
			Skip("default NodePool explicitly sets osImageStream=" + defaultNP.Spec.OSImageStream.Name + "; skipping default-resolution test")
		}

		// The default OS stream is currently hardcoded to rhel-9 for all OCP versions.
		// TODO(CNTRLPLANE-3032): When the hardcoding is removed, change this to expect rhel-10
		// on OCP >= 5.0.
		expectedStream := hyperv1.OSImageStreamRHEL9

		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			"default NodePool status to report osImageStream="+expectedStream,
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.OSImageStreamPredicate(expectedStream),
			},
			e2eutil.WithTimeout(5*time.Minute),
			e2eutil.WithInterval(10*time.Second),
		)
	})
}

// conditionMessageContains returns a predicate that checks whether a NodePool
// condition of the given type has a message containing the specified substring.
func conditionMessageContains(condType string, substring string) e2eutil.Predicate[*hyperv1.NodePool] {
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		for _, cond := range pool.Status.Conditions {
			if cond.Type == condType {
				if strings.Contains(cond.Message, substring) {
					return true, fmt.Sprintf("condition %s message contains %q", condType, substring), nil
				}
				return false, fmt.Sprintf("condition %s message %q does not contain %q", condType, cond.Message, substring), nil
			}
		}
		return false, fmt.Sprintf("%s condition not found", condType), nil
	}
}

// NodePoolOSImageStreamExplicitDefaultNoRolloutTest verifies that setting
// osImageStream explicitly to the version-derived default does not trigger
// a rollout. The controller normalizes the explicit value against the default
// and keeps the config hash unchanged.
// This is a lifecycle test: it mutates spec.osImageStream on the default NodePool
// and restores it on cleanup.
func NodePoolOSImageStreamExplicitDefaultNoRolloutTest(getTestCtx internal.TestContextGetter) {
	It("When osImageStream is set to the version-derived default, it should not trigger a rollout", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedCluster()

		hc := testCtx.GetHostedCluster()
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// This test verifies that setting the explicit stream to the default doesn't
		// trigger a rollout — skip if already explicitly set.
		if defaultNP.Spec.OSImageStream.Name != "" {
			Skip("default NodePool already has explicit osImageStream=" + defaultNP.Spec.OSImageStream.Name)
		}

		// Record the current config hash before mutation.
		originalConfigHash := defaultNP.Annotations[nodePoolAnnotationCurrentConfig]
		Expect(originalConfigHash).NotTo(BeEmpty(),
			"default NodePool %s should have a config hash annotation", defaultNP.Name)
		GinkgoWriter.Printf("Original config hash for NodePool %s: %s\n", defaultNP.Name, originalConfigHash)

		// Determine the version-derived default stream.
		// On OCP < 5.0: rhel-9; on OCP >= 5.0: rhel-10 (default NodePool has no runc config).
		versionDerivedDefault := hyperv1.OSImageStreamRHEL9
		if e2eutil.IsGreaterThanOrEqualTo(e2eutil.Version50) {
			versionDerivedDefault = hyperv1.OSImageStreamRHEL10
		}
		GinkgoWriter.Printf("Version-derived default stream: %s\n", versionDerivedDefault)

		// Patch the NodePool to set osImageStream to the version-derived default.
		base := defaultNP.DeepCopy()
		defaultNP.Spec.OSImageStream.Name = versionDerivedDefault
		Expect(testCtx.MgmtClient.Patch(ctx, defaultNP, crclient.MergeFrom(base))).To(Succeed(),
			"failed to patch NodePool %s with osImageStream=%s", defaultNP.Name, versionDerivedDefault)
		GinkgoWriter.Printf("Patched NodePool %s with osImageStream=%s\n", defaultNP.Name, versionDerivedDefault)

		// Restore the original state on cleanup.
		DeferCleanup(func() {
			current := &hyperv1.NodePool{}
			if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), current); err != nil {
				if !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("Warning: failed to get NodePool %s for cleanup: %v\n", defaultNP.Name, err)
				}
				return
			}
			cleanupBase := current.DeepCopy()
			current.Spec.OSImageStream.Name = ""
			Expect(testCtx.MgmtClient.Patch(ctx, current, crclient.MergeFrom(cleanupBase))).To(Succeed(),
				"cleanup: failed to restore NodePool %s osImageStream", defaultNP.Name)
			GinkgoWriter.Printf("Restored NodePool %s osImageStream to unset\n", defaultNP.Name)
		})

		// Verify the config hash does not change over time.
		// The controller normalizes the explicit default to empty for hash computation,
		// so the hash should remain identical — no rollout triggered.
		Consistently(func(g Gomega) {
			pool := &hyperv1.NodePool{}
			g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), pool)).To(Succeed())
			g.Expect(pool.Annotations).To(HaveKeyWithValue(nodePoolAnnotationCurrentConfig, originalConfigHash),
				"config hash should not change when setting osImageStream to the version-derived default")
		}).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
	})
}
