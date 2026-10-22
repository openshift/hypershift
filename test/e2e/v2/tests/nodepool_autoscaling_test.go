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

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/utils/ptr"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// autoscalingTestLabelKey is the node label key used across autoscaling tests to
// constrain workload pods to specific NodePool nodes via nodeSelector.
const autoscalingTestLabelKey = "e2e-autoscaling-test"

// AutoscalingScaleUpDownTest tests autoscaling scale-up and scale-down behavior
func AutoscalingScaleUpDownTest(getTestCtx internal.TestContextGetter) {
	It("should scale up when workload increases and scale down when workload decreases", Label("nodepool-autoscaling-scale-up-down"), func() {
		testCtx := getTestCtx()
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context
		configureAutoscalerScaleDown(testCtx, hc)

		// Find the default NodePool to copy platform config
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create autoscaling NodePool with min=1, max=3 and a unique node label
		// so the workload targets only this NodePool's nodes.
		autoscalingLabel := map[string]string{autoscalingTestLabelKey: "scale-up-down"}
		autoscalingNP := buildAutoscalingNodePool(defaultNP, 1, 3, autoscalingLabel)
		err = testCtx.MgmtClient.Create(ctx, autoscalingNP)
		Expect(err).NotTo(HaveOccurred(), "failed to create autoscaling NodePool")
		GinkgoWriter.Printf("Created autoscaling NodePool %s with min=1, max=3\n", autoscalingNP.Name)

		// Ensure cleanup
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP)
		})

		npLabelSelector := crclient.MatchingLabels{hyperv1.NodePoolLabel: autoscalingNP.Name}

		// Wait for NodePool to be ready with 1 node (min replicas)
		nodes := waitForAutoscalingNodes(ctx, hcClient, 1, npLabelSelector)
		Expect(nodes).To(HaveLen(1), "should have exactly 1 node initially")

		// Get node capacity for workload sizing
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		bytes, ok := memCapacity.AsInt64()
		Expect(ok).To(BeTrue(), "memory capacity should be convertible to int64")

		// Create workload that requires 3 nodes (50% memory per pod, 3 pods).
		// nodeSelector forces pods onto the autoscaling NodePool so the
		// cluster autoscaler must scale it up.
		workloadMemRequest := *resource.NewQuantity(bytes/2, resource.BinarySI)
		workload := newAutoscalingWorkload(3, workloadMemRequest, autoscalingLabel)
		err = hcClient.Create(ctx, workload)
		Expect(err).NotTo(HaveOccurred(), "failed to create workload")

		DeferCleanup(func() {
			cleanupWorkload(ctx, hcClient, workload)
		})

		// Wait for scale-up to 3 nodes
		waitForAutoscalingNodes(ctx, hcClient, 3, npLabelSelector)

		// Delete workload to trigger scale-down
		cleanupWorkload(ctx, hcClient, workload)

		// Wait for scale-down to 1 node (min replicas)
		waitForAutoscalingNodes(ctx, hcClient, 1, npLabelSelector)
	})
}

// AutoscalingBalancingTest tests that autoscaling balances workload across multiple NodePools.
// It configures the HostedCluster with the Random expander so the cluster autoscaler
// distributes scale-up events across NodePools instead of favoring one.
func AutoscalingBalancingTest(getTestCtx internal.TestContextGetter) {
	It("should balance pods across multiple autoscaling NodePools", Label("nodepool-autoscaling-balancing"), func() {
		testCtx := getTestCtx()

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		skipIfNativeOriginBelow(testCtx, semver.MustParse("4.20.0"))

		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		balancingLabel := "e2e-balance-ignore"
		originalNodes := &corev1.NodeList{}
		Expect(hcClient.List(ctx, originalNodes)).To(Succeed())
		originalHC := hc.DeepCopy()
		hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
			Scaling: hyperv1.ScaleUpAndScaleDown,
			ScaleDown: &hyperv1.ScaleDownConfig{
				DelayAfterAddSeconds:        ptr.To[int32](300),
				UnneededDurationSeconds:     ptr.To[int32](600),
				UtilizationThresholdPercent: ptr.To[int32](50),
			},
			Expanders: []hyperv1.ExpanderString{
				hyperv1.RandomExpander,
			},
			BalancingIgnoredLabels: []string{
				balancingLabel,
			},
			MaxFreeDifferenceRatioPercent: ptr.To[int32](70),
			MaxNodesTotal:                 ptr.To(int32(len(originalNodes.Items) + 6)),
		}
		err = testCtx.MgmtClient.Patch(ctx, hc, crclient.MergeFrom(originalHC))
		Expect(err).NotTo(HaveOccurred(), "failed to configure autoscaler on HostedCluster")
		GinkgoWriter.Println("Configured HostedCluster autoscaling with Random expander")

		DeferCleanup(func() {
			latest := &hyperv1.HostedCluster{}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), latest)).To(Succeed(),
				"cleanup: failed to get HostedCluster for autoscaling config reset")
			patch := crclient.MergeFrom(latest.DeepCopy())
			latest.Spec.Autoscaling = originalHC.Spec.Autoscaling
			Expect(testCtx.MgmtClient.Patch(ctx, latest, patch)).To(Succeed(),
				"cleanup: failed to reset autoscaler config on HostedCluster")
		})

		// Find the default NodePool to copy platform config
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create two autoscaling NodePools with distinct labels for the
		// balancing-ignored-labels config and a shared label for the workload nodeSelector.
		sharedLabel := map[string]string{autoscalingTestLabelKey: "balance"}
		np1Labels := map[string]string{
			autoscalingTestLabelKey: "balance",
			balancingLabel:          "np1",
		}
		np2Labels := map[string]string{
			autoscalingTestLabelKey: "balance",
			balancingLabel:          "np2",
		}

		autoscalingNP1 := buildAutoscalingNodePool(defaultNP, 1, 4, np1Labels)
		err = testCtx.MgmtClient.Create(ctx, autoscalingNP1)
		Expect(err).NotTo(HaveOccurred(), "failed to create first autoscaling NodePool")
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP1)
		})

		autoscalingNP2 := buildAutoscalingNodePool(defaultNP, 1, 4, np2Labels)
		err = testCtx.MgmtClient.Create(ctx, autoscalingNP2)
		Expect(err).NotTo(HaveOccurred(), "failed to create second autoscaling NodePool")
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP2)
		})

		// The autoscaler is enabled only when an autoscaling NodePool exists.
		// Create the NodePools before waiting for the configured deployment.
		waitForAutoscalerSettings(ctx, testCtx.MgmtClient, cpNamespace, []string{
			"--expander=random", "--balancing-ignore-label=" + balancingLabel,
			"--balance-similar-node-groups=true", "--scale-down-enabled=true",
			"--scale-down-delay-after-add=300s", "--scale-down-unneeded-time=600s",
			"--scale-down-utilization-threshold=0.50", "--max-free-difference-ratio=0.70",
			fmt.Sprintf("--max-nodes-total=%d", len(originalNodes.Items)+6),
		})
		for _, pool := range []*hyperv1.NodePool{autoscalingNP1, autoscalingNP2} {
			Eventually(func(g Gomega) {
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pool), pool)).To(Succeed())
				g.Expect(pool.Status.Conditions).To(ContainElement(And(HaveField("Type", hyperv1.NodePoolAutoscalingEnabledConditionType), HaveField("Status", corev1.ConditionTrue))))
			}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		}

		np1LabelSelector := crclient.MatchingLabels{hyperv1.NodePoolLabel: autoscalingNP1.Name}
		np2LabelSelector := crclient.MatchingLabels{hyperv1.NodePoolLabel: autoscalingNP2.Name}

		// Wait for initial nodes (1 per NodePool at min replicas)
		nodes := waitForAutoscalingNodes(ctx, hcClient, 1, np1LabelSelector)
		waitForAutoscalingNodes(ctx, hcClient, 1, np2LabelSelector)

		// Get node capacity for workload sizing
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		bytes, ok := memCapacity.AsInt64()
		Expect(ok).To(BeTrue(), "memory capacity should be convertible to int64")

		// Create workload targeting the autoscaling NodePools via the shared label.
		workloadMemRequest := *resource.NewQuantity(bytes/2, resource.BinarySI)
		workload := newAutoscalingWorkload(6, workloadMemRequest, sharedLabel)
		err = hcClient.Create(ctx, workload)
		Expect(err).NotTo(HaveOccurred(), "failed to create workload")
		DeferCleanup(func() {
			cleanupWorkload(ctx, hcClient, workload)
		})

		Eventually(func() (bool, error) {
			if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalingNP1), autoscalingNP1); err != nil {
				return false, err
			}
			if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalingNP2), autoscalingNP2); err != nil {
				return false, err
			}

			total := autoscalingNP1.Status.Replicas + autoscalingNP2.Status.Replicas
			if total != 6 {
				return false, nil
			}
			return autoscalingNP1.Status.Replicas >= 2 && autoscalingNP2.Status.Replicas >= 2, nil
		}).WithContext(ctx).WithTimeout(30*time.Minute).
			WithPolling(30*time.Second).
			Should(BeTrue(), "NodePools should have balanced distribution")
		waitForAutoscalingNodes(ctx, hcClient, int(autoscalingNP1.Status.Replicas), np1LabelSelector)
		waitForAutoscalingNodes(ctx, hcClient, int(autoscalingNP2.Status.Replicas), np2LabelSelector)
		cleanupWorkload(ctx, hcClient, workload)
		waitForAutoscalingNodes(ctx, hcClient, 1, np1LabelSelector)
		waitForAutoscalingNodes(ctx, hcClient, 1, np2LabelSelector)
	})
}

// AutoscalingScaleFromZeroTest tests that a NodePool with min=0 can scale from
// zero to at least one node when a workload is created. This requires
// scale-from-zero to be enabled on the HyperShift Operator with the appropriate
// cloud credentials (--scale-from-zero-provider flag). The test verifies that
// capacity information is propagated to the MachineDeployment via annotations
// so the cluster autoscaler can determine node sizing without running instances.
//
// Supported platforms: AWS, Azure. Skips on other platforms or when the
// scale-from-zero credentials secret is not present.
func AutoscalingScaleFromZeroTest(getTestCtx internal.TestContextGetter) {
	It("should scale from zero when a workload is created", Label("nodepool-autoscaling-scale-from-zero"), func() {
		testCtx := getTestCtx()
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

		// Platform guard: scale-from-zero is only supported on AWS and Azure
		if hc.Spec.Platform.Type != hyperv1.AWSPlatform && hc.Spec.Platform.Type != hyperv1.AzurePlatform {
			Skip("scale-from-zero test only supported on AWS and Azure platforms")
		}

		ctx := testCtx.Context

		// Check for scale-from-zero credentials secret — this is created when
		// the HyperShift operator is started with --scale-from-zero-provider
		// and --scale-from-zero-creds flags.
		scaleFromZeroSecret := &corev1.Secret{}
		err = testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: "hypershift",
			Name:      "hypershift-operator-scale-from-zero-credentials",
		}, scaleFromZeroSecret)
		if apierrors.IsNotFound(err) {
			Skip("scale-from-zero not enabled (secret hypershift-operator-scale-from-zero-credentials not found)")
		}
		Expect(err).NotTo(HaveOccurred(), "failed to check for scale-from-zero secret")

		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred(), "failed to create hosted-cluster client for %s/%s", hc.Namespace, hc.Name)

		// Find the default NodePool to copy platform config
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create autoscaling NodePool with min=0, max=2 and a unique node label
		// so the workload targets only this NodePool's nodes.
		scaleFromZeroLabel := map[string]string{autoscalingTestLabelKey: "scale-from-zero"}
		scaleFromZeroNP := buildAutoscalingNodePool(defaultNP, 0, 2, scaleFromZeroLabel)
		err = testCtx.MgmtClient.Create(ctx, scaleFromZeroNP)
		Expect(err).NotTo(HaveOccurred(), "failed to create scale-from-zero NodePool")
		GinkgoWriter.Printf("Created scale-from-zero NodePool %s with min=0, max=2\n", scaleFromZeroNP.Name)

		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, scaleFromZeroNP)
		})

		// Verify capacity information is available on the MachineDeployment.
		// The scale-from-zero controller sets platform-agnostic annotations
		// (vCPU, memoryMb, capacity labels) so the cluster autoscaler can
		// determine node sizing without running instances.
		cpNamespace := testCtx.ControlPlaneNamespace
		Eventually(func(g Gomega) {
			deployment := &capiv1.MachineDeployment{}
			g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: cpNamespace, Name: scaleFromZeroNP.Name}, deployment)).To(Succeed())
			var nativeCapacity corev1.ResourceList
			if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
				template := &capiaws.AWSMachineTemplate{}
				g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: cpNamespace, Name: deployment.Spec.Template.Spec.InfrastructureRef.Name}, template)).To(Succeed())
				nativeCapacity = template.Status.Capacity
			}
			g.Expect(autoscalingCapacity(nativeCapacity, deployment.Annotations, scaleFromZeroNP.Spec.Arch)).To(Succeed())
		}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		md := &capiv1.MachineDeployment{}
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: cpNamespace,
			Name:      scaleFromZeroNP.Name,
		}, md)).To(Succeed(), "failed to get MachineDeployment %s/%s for capacity annotation logging", cpNamespace, scaleFromZeroNP.Name)
		GinkgoWriter.Printf("MachineDeployment %s has capacity annotations: vCPU=%s, memoryMb=%s\n",
			md.Name,
			md.Annotations["machine.openshift.io/vCPU"],
			md.Annotations["machine.openshift.io/memoryMb"])

		// Verify NodePool autoscaling is enabled
		Eventually(func(g Gomega) {
			current := &hyperv1.NodePool{}
			g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), current)).To(Succeed())
			g.Expect(current.Status.Conditions).To(ContainElement(And(
				HaveField("Type", hyperv1.NodePoolAutoscalingEnabledConditionType), HaveField("Status", corev1.ConditionTrue),
			)))
		}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		// Verify NodePool starts with 0 replicas
		GinkgoWriter.Println("Verifying NodePool starts with 0 replicas")
		initialNP := &hyperv1.NodePool{}
		err = testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), initialNP)
		Expect(err).NotTo(HaveOccurred(), "failed to get NodePool %s/%s before zero-replica assertion", scaleFromZeroNP.Namespace, scaleFromZeroNP.Name)
		Expect(initialNP.Status.Replicas).To(Equal(int32(0)), "NodePool %s/%s should start with 0 replicas", scaleFromZeroNP.Namespace, scaleFromZeroNP.Name)

		// Create workload to trigger scale-up from 0.
		// Use fixed resource requests (1Gi memory, 500m CPU) since there are no
		// existing nodes to measure capacity from. The nodeSelector targets only
		// this NodePool's nodes so the autoscaler scales this specific pool.
		GinkgoWriter.Println("Creating workload to trigger scale-up from 0 nodes")
		workload := newAutoscalingWorkload(2, resource.MustParse("1Gi"), scaleFromZeroLabel)
		err = hcClient.Create(ctx, workload)
		Expect(err).NotTo(HaveOccurred(), "failed to create scale-from-zero workload")
		GinkgoWriter.Printf("Created workload %s with 2 pods\n", workload.Name)

		DeferCleanup(func() {
			cleanupWorkload(ctx, hcClient, workload)
		})

		// Wait for NodePool to scale from 0 to at least 1 replica.
		// The 2 workload pods may fit on a single node depending on instance
		// capacity, so we only require ≥1 rather than exactly 2.
		GinkgoWriter.Println("Waiting for NodePool to scale from 0 to at least 1 node")
		Eventually(func(g Gomega) {
			current := &hyperv1.NodePool{}
			g.Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), current)).To(Succeed())
			g.Expect(current.Status.Replicas).To(BeNumerically(">", 0))
		}).WithContext(ctx).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		scaledNP := &hyperv1.NodePool{}
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), scaledNP)).To(Succeed(),
			"failed to get NodePool %s/%s after scaling from zero", scaleFromZeroNP.Namespace, scaleFromZeroNP.Name)
		GinkgoWriter.Printf("NodePool successfully scaled from 0 to %d replicas\n", scaledNP.Status.Replicas)

		// Wait for at least 1 ready node from the scale-from-zero NodePool
		npLabelSelector := crclient.MatchingLabels{hyperv1.NodePoolLabel: scaleFromZeroNP.Name}
		Eventually(func(g Gomega) {
			nodes := &corev1.NodeList{}
			g.Expect(hcClient.List(ctx, nodes, npLabelSelector)).To(Succeed())
			g.Expect(nodes.Items).NotTo(BeEmpty())
			for _, node := range nodes.Items {
				g.Expect(node.DeletionTimestamp).To(BeNil())
				g.Expect(node.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.NodeReady), HaveField("Status", corev1.ConditionTrue))))
			}
		}).WithContext(ctx).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			pods := &corev1.PodList{}
			g.Expect(hcClient.List(ctx, pods, crclient.InNamespace(workload.Namespace), crclient.MatchingLabels{batchv1.JobNameLabel: workload.Name})).To(Succeed())
			g.Expect(pods.Items).To(HaveLen(2))
			for _, pod := range pods.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				g.Expect(pod.Spec.NodeName).NotTo(BeEmpty())
				g.Expect(pod.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
			}
		}).WithContext(ctx).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		GinkgoWriter.Println("Successfully verified scale-from-zero: NodePool scaled and node is ready")
	})
}

// Helper functions

// getDefaultNodePool finds a NodePool for the hosted cluster that is not being
// deleted and has at least one ready replica (status.replicas). It is used both
// as a template for platform config and for inspecting a backing node, so it
// skips NodePools with a deletion timestamp (e.g. test NodePools mid-teardown)
// and those without a ready node. Returns nil if none qualifies.
func getDefaultNodePool(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) *hyperv1.NodePool {
	GinkgoHelper()

	npList := &hyperv1.NodePoolList{}
	Expect(client.List(ctx, npList, crclient.InNamespace(hc.Namespace))).To(Succeed(),
		"failed to list NodePools for HostedCluster %s/%s", hc.Namespace, hc.Name)
	Expect(npList.Items).NotTo(BeEmpty(), "should have at least one NodePool")

	// Find a live NodePool for this HostedCluster, skipping any that are
	// terminating or have no ready replica.
	for i := range npList.Items {
		np := &npList.Items[i]
		if np.Spec.ClusterName != hc.Name {
			continue
		}
		if np.DeletionTimestamp != nil {
			continue
		}
		if np.Status.Replicas < 1 {
			continue
		}
		return np
	}

	return nil
}

// buildAutoscalingNodePool creates a new NodePool with autoscaling enabled based on a template.
// nodeLabels are applied to the NodePool's nodes so workloads can target them with a nodeSelector.
func buildAutoscalingNodePool(template *hyperv1.NodePool, min, max int32, nodeLabels map[string]string) *hyperv1.NodePool {
	GinkgoHelper()

	name := names.SimpleNameGenerator.GenerateName(template.Spec.ClusterName + "-auto-")
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: template.Namespace,
		},
	}

	// Deep copy the spec from template
	template.Spec.DeepCopyInto(&np.Spec)

	// Configure autoscaling
	np.Spec.Replicas = nil // Must be nil when using autoscaling
	np.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
		Min: ptr.To(min),
		Max: max,
	}

	if len(nodeLabels) > 0 {
		if np.Spec.NodeLabels == nil {
			np.Spec.NodeLabels = make(map[string]string)
		}
		for k, v := range nodeLabels {
			np.Spec.NodeLabels[k] = v
		}
	}

	return np
}

// newAutoscalingWorkload creates a Job that spawns multiple pods for autoscaling tests.
// nodeSelector constrains pods to land on specific NodePool nodes so the
// cluster autoscaler is forced to scale the targeted NodePool.
func newAutoscalingWorkload(njobs int32, memoryRequest resource.Quantity, nodeSelector map[string]string) *batchv1.Job {
	GinkgoHelper()

	name := names.SimpleNameGenerator.GenerateName("autoscaling-workload-")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "workload",
							Image: "registry.access.redhat.com/ubi9/ubi-minimal:latest",
							Command: []string{
								"sleep",
								"86400", // 1 day
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"memory": memoryRequest,
									"cpu":    resource.MustParse("500m"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								RunAsNonRoot: ptr.To(false),
								RunAsUser:    ptr.To(int64(0)),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					NodeSelector:  nodeSelector,
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
			BackoffLimit: ptr.To[int32](4),
			Completions:  ptr.To(njobs),
			Parallelism:  ptr.To(njobs),
		},
	}

	return job
}

// cleanupNodePool deletes a NodePool if it exists
func cleanupNodePool(ctx context.Context, client crclient.Client, np *hyperv1.NodePool) {
	GinkgoHelper()
	timeout := nodePoolUpgradeTimeout(np.Spec.Platform.Type)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout+time.Minute)
	defer cancel()

	err := client.Delete(ctx, np)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", np.Name)
	Eventually(func() bool {
		err := client.Get(ctx, crclient.ObjectKeyFromObject(np), &hyperv1.NodePool{})
		Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), "cleanup: failed to get NodePool: %v", err)
		return apierrors.IsNotFound(err)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15 * time.Second).Should(BeTrue())
}

// cleanupWorkload deletes a Job workload if it exists
func cleanupWorkload(ctx context.Context, client crclient.Client, job *batchv1.Job) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()

	cascadeDelete := metav1.DeletePropagationForeground
	err := client.Delete(ctx, job, &crclient.DeleteOptions{
		PropagationPolicy: &cascadeDelete,
	})
	Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), "cleanup: failed to delete workload %s: %v", job.Name, err)
	if err == nil {
		GinkgoWriter.Printf("Deleted workload %s\n", job.Name)
	}
	Eventually(func(g Gomega) {
		g.Expect(apierrors.IsNotFound(client.Get(ctx, crclient.ObjectKeyFromObject(job), &batchv1.Job{}))).To(BeTrue())
		pods := &corev1.PodList{}
		g.Expect(client.List(ctx, pods, crclient.InNamespace(job.Namespace), crclient.MatchingLabels{batchv1.JobNameLabel: job.Name})).To(Succeed())
		g.Expect(pods.Items).To(BeEmpty())
	}).WithContext(ctx).WithTimeout(4*time.Minute).WithPolling(5*time.Second).Should(Succeed(), "cleanup: workload and pods must be gone before observing scale-down")
}

// RegisterNodePoolAutoscalingTests registers all autoscaling test cases
func RegisterNodePoolAutoscalingTests(getTestCtx internal.TestContextGetter) {
	AutoscalingScaleUpDownTest(getTestCtx)
	AutoscalingPauseTest(getTestCtx)
	AutoscalingBalancingTest(getTestCtx)
	AutoscalingScaleFromZeroTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolAutoscaling] NodePool Autoscaling", Label("lifecycle", "nodepool-autoscaling"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodePoolAutoscalingTests(func() *internal.TestContext { return testCtx })
})
