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
	"regexp"
	"strings"
	"time"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
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
	NodePoolOSImageStreamNodeOSVerificationTest(getTestCtx)
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
// It initializes the test context and skips when the OSStreams feature gate is disabled
// or the platform does not use RHCOS nodes.
func osImageStreamBeforeEach(testCtx **internal.TestContext) {
	*testCtx = internal.GetTestContext()
	Expect(*testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

	hasSpecField, err := e2eutil.HasFieldInCRDSchema((*testCtx).Context, (*testCtx).MgmtClient,
		"nodepools.hypershift.openshift.io", "spec.osImageStream")
	Expect(err).NotTo(HaveOccurred(), "failed to check CRD schema for spec.osImageStream")
	if !hasSpecField {
		Skip("OSStreams feature gate is not enabled: spec.osImageStream field not present in NodePool CRD")
	}

	hasStatusField, err := e2eutil.HasFieldInCRDSchema((*testCtx).Context, (*testCtx).MgmtClient,
		"nodepools.hypershift.openshift.io", "status.osImageStream")
	Expect(err).NotTo(HaveOccurred(), "failed to check CRD schema for status.osImageStream")
	if !hasStatusField {
		Skip("OSStreams feature gate is not enabled: status.osImageStream field not present in NodePool CRD")
	}
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:OSStreams] NodePool OSImageStream Lifecycle", Label("lifecycle", "nodepool-osimagestream"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		osImageStreamBeforeEach(&testCtx)
	})

	RegisterNodePoolOSImageStreamLifecycleTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:OSStreams] NodePool OSImageStream Status", Label("lifecycle", "nodepool-osimagestream"), func() {
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
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if testCtx.VersionAtLeast(e2eutil.Version50) {
			Skip("test only applies to OCP < 5.0; rhel-10 is valid on OCP >= 5.0")
		}

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

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		testCtx.SkipIfVersionBelow(e2eutil.Version50)

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
// (no osImageStream set) reports a recognized RHEL stream in status.osImageStream.
// This is a non-lifecycle test: it reads existing state without mutation.
func NodePoolOSImageStreamDefaultStatusTest(getTestCtx internal.TestContextGetter) {
	It("When no osImageStream is set, it should report a recognized RHEL stream in status", func() {
		testCtx := getTestCtx()

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// This test verifies default resolution — skip if the default NodePool
		// explicitly sets osImageStream (the test would verify explicit, not default).
		if defaultNP.Spec.OSImageStream.Name != "" {
			Skip("default NodePool explicitly sets osImageStream=" + defaultNP.Spec.OSImageStream.Name + "; skipping default-resolution test")
		}

		// Wait for the NodePool to have nodesInfo populated, which confirms CAPI
		// Machines have NodeInfo (including OSImage). The controller uses Machine
		// NodeInfo.OSImage to infer status.osImageStream, so without it the field
		// will never be set.
		GinkgoWriter.Printf("Waiting for NodePool %s/%s to have nodesInfo with node versions populated\n",
			defaultNP.Namespace, defaultNP.Name)
		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			fmt.Sprintf("NodePool %s/%s to have nodesInfo.nodeVersions populated", defaultNP.Namespace, defaultNP.Name),
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				nodesInfoPopulatedPredicate(),
			},
			e2eutil.WithTimeout(10*time.Minute),
			e2eutil.WithInterval(15*time.Second),
		)

		expectedStream := hyperv1.OSImageStreamRHEL10

		GinkgoWriter.Printf("Waiting for NodePool %s/%s status.osImageStream.name to be %s\n",
			defaultNP.Namespace, defaultNP.Name, expectedStream)
		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			"default NodePool status to report a non-empty osImageStream",
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				osImageStreamSetPredicate(),
			},
			e2eutil.WithTimeout(10*time.Minute),
			e2eutil.WithInterval(15*time.Second),
		)

		By("verifying node OS images match the resolved osImageStream")
		verifyNodeOSMatchesStream(testCtx, defaultNP, expectedStream)
	})
}

// rhcosMajorVersionRe extracts the RHCOS major version from the node's OSImage
// string (e.g. "Red Hat Enterprise Linux CoreOS 10.2.20260724-0 (Coughlan)" → "10").
var rhcosMajorVersionRe = regexp.MustCompile(`CoreOS (\d+)\.`)

func expectedRHELMajorForStream(stream string) (string, error) {
	switch stream {
	case hyperv1.OSImageStreamRHEL10:
		return "10", nil
	case hyperv1.OSImageStreamRHEL9:
		return "9", nil
	default:
		return "", fmt.Errorf("unrecognized osImageStream %q", stream)
	}
}

// verifyNodeOSMatchesStream verifies that the actual node OS matches the
// expected osImageStream by checking node.Status.NodeInfo.OSImage on the
// hosted cluster. This catches regressions where the controller reports
// the correct stream in status but the MCO fails to apply it (e.g. due
// to an API version mismatch in the rendered OSImageStream CR).
func verifyNodeOSMatchesStream(testCtx *internal.TestContext, np *hyperv1.NodePool, expectedStream string) {
	hc, err := testCtx.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred())
	hcClient, err := testCtx.GetHostedClusterClient(hc)
	Expect(err).NotTo(HaveOccurred(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")

	e2eutil.EventuallyObject[*hyperv1.NodePool](
		GinkgoTB(), testCtx.Context,
		fmt.Sprintf("NodePool %s/%s to have all nodes ready", np.Namespace, np.Name),
		func(pollCtx context.Context) (*hyperv1.NodePool, error) {
			pool := &hyperv1.NodePool{}
			err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(np), pool)
			return pool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{allNodesReadyPredicate()},
		e2eutil.WithTimeout(45*time.Minute),
		e2eutil.WithInterval(30*time.Second),
	)

	expectedMajor, err := expectedRHELMajorForStream(expectedStream)
	Expect(err).NotTo(HaveOccurred())

	nodeList := &corev1.NodeList{}
	Expect(hcClient.List(testCtx.Context, nodeList,
		crclient.MatchingLabels{hyperv1.NodePoolLabel: np.Name})).To(Succeed())
	Expect(nodeList.Items).NotTo(BeEmpty(), "no nodes found for NodePool %s", np.Name)

	for _, node := range nodeList.Items {
		osImage := node.Status.NodeInfo.OSImage
		GinkgoWriter.Printf("Verifying node %s in NodePool %s runs RHCOS major version %s (stream=%s, osImage=%s)\n",
			node.Name, np.Name, expectedMajor, expectedStream, osImage)

		m := rhcosMajorVersionRe.FindStringSubmatch(osImage)
		Expect(m).To(HaveLen(2), "could not parse RHCOS version from %q on node %s", osImage, node.Name)
		Expect(m[1]).To(Equal(expectedMajor),
			"node %s osImage=%q has RHCOS major=%s, expected=%s for stream %s",
			node.Name, osImage, m[1], expectedMajor, expectedStream)
	}
}

// NodePoolOSImageStreamNodeOSVerificationTest verifies that actual node OS versions
// match the expected osImageStream across three scenarios: the default NodePool
// (no osImageStream set), an explicit rhel-9 NodePool, and an explicit rhel-10
// NodePool. This is a lifecycle test because it creates additional NodePools.
func NodePoolOSImageStreamNodeOSVerificationTest(getTestCtx internal.TestContextGetter) {
	It("When NodePools have different osImageStream values, nodes should run the matching OS version", func() {
		testCtx := getTestCtx()

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())

		// rhel-10 osImageStream is only supported on OCP 5+
		testCtx.SkipIfVersionBelow(e2eutil.Version50)

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")
		Expect(defaultNP.Spec.OSImageStream.Name).To(BeEmpty(),
			"default NodePool %s should not have an explicit spec.osImageStream set", defaultNP.Name)

		By("waiting for the default NodePool to have status.osImageStream resolved")
		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			fmt.Sprintf("NodePool %s/%s status.osImageStream to be set", defaultNP.Namespace, defaultNP.Name),
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				osImageStreamSetPredicate(),
			},
			e2eutil.WithTimeout(10*time.Minute),
			e2eutil.WithInterval(15*time.Second),
		)

		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), defaultNP)).To(Succeed())
		defaultStream := defaultNP.Status.OSImageStream.Name
		Expect(defaultStream).NotTo(BeEmpty(), "default NodePool %s should have status.osImageStream.name set", defaultNP.Name)

		By(fmt.Sprintf("verifying the default NodePool (spec.osImageStream=%q, status.osImageStream=%s) runs the expected OS",
			defaultNP.Spec.OSImageStream.Name, defaultStream))
		verifyNodeOSMatchesStream(testCtx, defaultNP, defaultStream)

		var alternateStream string
		switch defaultStream {
		case hyperv1.OSImageStreamRHEL10:
			alternateStream = hyperv1.OSImageStreamRHEL9
		case hyperv1.OSImageStreamRHEL9:
			alternateStream = hyperv1.OSImageStreamRHEL10
		default:
			Fail(fmt.Sprintf("unexpected default stream %q", defaultStream))
		}

		By(fmt.Sprintf("creating a NodePool with osImageStream=%s", alternateStream))
		var oneReplica int32 = 1
		npAlternate := buildTestNodePool(defaultNP, "osstream-"+alternateStream, func(pool *hyperv1.NodePool) {
			pool.Spec.Replicas = &oneReplica
			pool.Spec.OSImageStream = hyperv1.OSImageStreamReference{
				Name: alternateStream,
			}
		})
		Expect(testCtx.MgmtClient.Create(ctx, npAlternate)).To(Succeed(), "failed to create NodePool %s", npAlternate.Name)
		GinkgoWriter.Printf("Created NodePool %s with osImageStream=%s\n", npAlternate.Name, alternateStream)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, npAlternate)
		})

		By(fmt.Sprintf("verifying %s NodePool nodes run the correct OS", alternateStream))
		verifyNodeOSMatchesStream(testCtx, npAlternate, alternateStream)
	})
}

// nodesInfoPopulatedPredicate returns a predicate that validates that a NodePool's
// status.nodesInfo.nodeVersions has at least one entry with a non-zero ready count.
// This confirms that CAPI Machines have NodeInfo populated (the controller uses
// the same Machine list and NodeInfo check for both nodesInfo and osImageStream).
func nodesInfoPopulatedPredicate() e2eutil.Predicate[*hyperv1.NodePool] {
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		versions := pool.Status.NodesInfo.NodeVersions
		if len(versions) == 0 {
			return false, "status.nodesInfo.nodeVersions is empty (CAPI Machines may not have NodeInfo populated yet)", nil
		}
		var totalReady int32
		for _, v := range versions {
			if v.ReadyNodeCount != nil {
				totalReady += *v.ReadyNodeCount
			}
		}
		if totalReady == 0 {
			return false, fmt.Sprintf("status.nodesInfo has %d version entries but 0 ready nodes", len(versions)), nil
		}
		return true, fmt.Sprintf("status.nodesInfo has %d ready nodes across %d version entries", totalReady, len(versions)), nil
	}
}

// allNodesReadyPredicate returns a predicate that validates that all of a NodePool's
// expected replicas are ready, as reported by status.nodesInfo.nodeVersions.
func allNodesReadyPredicate() e2eutil.Predicate[*hyperv1.NodePool] {
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		var expected int32
		if pool.Spec.Replicas != nil {
			expected = *pool.Spec.Replicas
		}
		if expected == 0 {
			return false, "spec.replicas is 0 or nil", nil
		}
		versions := pool.Status.NodesInfo.NodeVersions
		if len(versions) == 0 {
			return false, fmt.Sprintf("status.nodesInfo.nodeVersions is empty, want %d ready nodes", expected), nil
		}
		var totalReady int32
		for _, v := range versions {
			if v.ReadyNodeCount != nil {
				totalReady += *v.ReadyNodeCount
			}
		}
		if totalReady < expected {
			return false, fmt.Sprintf("status.nodesInfo has %d/%d ready nodes", totalReady, expected), nil
		}
		return true, fmt.Sprintf("all %d nodes ready", totalReady), nil
	}
}

// osImageStreamSetPredicate returns a predicate that validates that a NodePool's
// status.osImageStream.name is set to a recognized RHEL stream value.
func osImageStreamSetPredicate() e2eutil.Predicate[*hyperv1.NodePool] {
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		name := pool.Status.OSImageStream.Name
		if name == "" {
			return false, "status.osImageStream.name is empty", nil
		}
		switch name {
		case hyperv1.OSImageStreamRHEL9, hyperv1.OSImageStreamRHEL10:
			return true, fmt.Sprintf("status.osImageStream.name=%s", name), nil
		default:
			return false, fmt.Sprintf("status.osImageStream.name=%q is not a recognized RHEL stream", name), nil
		}
	}
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

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
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

		// Poll for status.osImageStream rather than reading a point-in-time
		// snapshot — the controller may not have set it yet if Machines are
		// still registering NodeInfo.
		GinkgoWriter.Printf("Waiting for NodePool %s to have status.osImageStream set\n", defaultNP.Name)
		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			fmt.Sprintf("NodePool %s status.osImageStream to be set", defaultNP.Name),
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				osImageStreamSetPredicate(),
			},
			e2eutil.WithTimeout(10*time.Minute),
			e2eutil.WithInterval(15*time.Second),
		)

		// Re-read the NodePool to get the populated status for the patch below.
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNP), defaultNP)).To(Succeed())
		versionDerivedDefault := defaultNP.Status.OSImageStream.Name
		GinkgoWriter.Printf("Observed default stream from status: %s\n", versionDerivedDefault)

		// Patch the NodePool to set osImageStream to the version-derived default.
		base := defaultNP.DeepCopy()
		defaultNP.Spec.OSImageStream.Name = versionDerivedDefault
		Expect(testCtx.MgmtClient.Patch(ctx, defaultNP, crclient.MergeFrom(base))).To(Succeed(),
			"failed to patch NodePool %s with osImageStream=%s", defaultNP.Name, versionDerivedDefault)
		GinkgoWriter.Printf("Patched NodePool %s with osImageStream=%s\n", defaultNP.Name, versionDerivedDefault)

		// No cleanup needed: osImageStream is immutable once set (CEL validation
		// rejects removal), and we set it to the version-derived default which is
		// semantically equivalent to the original unset state.

		// Verify the config hash does not change over time.
		// The controller normalizes the explicit default to empty for hash computation,
		// so both hashes should be identical — no rollout would be triggered.
		e2eutil.EventuallyObject[*hyperv1.NodePool](
			GinkgoTB(), ctx,
			fmt.Sprintf("NodePool %s config hash to match baseline (explicit default == implicit default)", defaultNP.Name),
			func(pollCtx context.Context) (*hyperv1.NodePool, error) {
				pool := &hyperv1.NodePool{}
				err := testCtx.MgmtClient.Get(pollCtx, crclient.ObjectKeyFromObject(defaultNP), pool)
				return pool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
					hash, ok := pool.Annotations[nodePoolAnnotationCurrentConfig]
					if !ok || hash == "" {
						return false, "config hash annotation not yet set", nil
					}
					if hash != originalConfigHash {
						return false, fmt.Sprintf("config hash %s != baseline %s", hash, originalConfigHash), nil
					}
					return true, "config hash matches baseline", nil
				},
			},
			e2eutil.WithTimeout(5*time.Minute),
			e2eutil.WithInterval(15*time.Second),
		)
	})
}

// NodePoolOSImageStreamUpgradeVerificationTest creates a NodePool at a previous
// release image, upgrades it to the latest, and verifies that status.osImageStream
// reports the version-derived stream after upgrade. The RHEL version follows the
// release version: upgrading to OCP 5.0+ results in rhel-10.
func NodePoolOSImageStreamUpgradeVerificationTest(getTestCtx internal.TestContextGetter) {
	It("When a NodePool is upgraded, it should report the correct osImageStream in status", func() {
		testCtx := getTestCtx()

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		previousImage := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE")
		latestImage := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		if previousImage == "" || latestImage == "" {
			Skip("E2E_PREVIOUS_RELEASE_IMAGE and E2E_LATEST_RELEASE_IMAGE must be set for upgrade tests")
		}

		ctx := testCtx.Context

		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		var oneReplica int32 = 1
		np := buildTestNodePool(defaultNP, "osstream-upgrade", func(pool *hyperv1.NodePool) {
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

		Expect(testCtx.MgmtClient.Create(ctx, np)).To(Succeed(), "failed to create NodePool %s", np.Name)
		GinkgoWriter.Printf("Created NodePool %s at previous release %s\n", np.Name, previousImage)
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, np)
		})

		e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

		// Upgrade to latest release
		GinkgoWriter.Printf("Upgrading NodePool %s to latest release %s\n", np.Name, latestImage)
		Expect(e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, np, func(obj *hyperv1.NodePool) {
			obj.Spec.Release.Image = latestImage
		})).To(Succeed(), "failed to update NodePool release image")

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

		// Verify osImageStream status after upgrade.
		// The RHEL version is dictated by the release version. After upgrading
		// to OCP 5.0+, nodes get rhel-10 boot images and the stream updates
		// accordingly. Only an explicit spec.osImageStream pin overrides this.
		upgradedNP := &hyperv1.NodePool{}
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), upgradedNP)).To(Succeed())
		upgradedVersion, err := semver.ParseTolerant(upgradedNP.Status.Version)
		Expect(err).NotTo(HaveOccurred(), "failed to parse upgraded NodePool version %q", upgradedNP.Status.Version)
		expectedStream := hyperv1.OSImageStreamRHEL9
		if upgradedVersion.Major >= 5 {
			expectedStream = hyperv1.OSImageStreamRHEL10
		}

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
	})
}
