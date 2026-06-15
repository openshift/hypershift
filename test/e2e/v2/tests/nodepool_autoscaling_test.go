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

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// AutoscalingScaleUpDownTest tests autoscaling scale-up and scale-down behavior
func AutoscalingScaleUpDownTest(getTestCtx internal.TestContextGetter) {
	It("should scale up when workload increases and scale down when workload decreases", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()
		ctx := testCtx.Context

		// Find the default NodePool to copy platform config
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create autoscaling NodePool with min=1, max=3 and a unique node label
		// so the workload targets only this NodePool's nodes.
		autoscalingLabel := map[string]string{"e2e-autoscaling-test": "scale-up-down"}
		autoscalingNP := buildAutoscalingNodePool(defaultNP, 1, 3, autoscalingLabel)
		err := testCtx.MgmtClient.Create(ctx, autoscalingNP)
		Expect(err).NotTo(HaveOccurred(), "failed to create autoscaling NodePool")
		GinkgoWriter.Printf("Created autoscaling NodePool %s with min=1, max=3\n", autoscalingNP.Name)

		// Ensure cleanup
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP)
		})

		npLabelSelector := e2eutil.WithClientOptions(crclient.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: autoscalingNP.Name}),
		})

		// Wait for NodePool to be ready with 1 node (min replicas)
		nodes := e2eutil.WaitForNReadyNodesWithOptions(GinkgoTB(), ctx, hcClient, 1, hc.Spec.Platform.Type, fmt.Sprintf("for NodePool %s", autoscalingNP.Name), npLabelSelector)
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
		e2eutil.WaitForNReadyNodesWithOptions(GinkgoTB(), ctx, hcClient, 3, hc.Spec.Platform.Type, fmt.Sprintf("for NodePool %s", autoscalingNP.Name), npLabelSelector)

		// Delete workload to trigger scale-down
		cleanupWorkload(ctx, hcClient, workload)

		// Wait for scale-down to 1 node (min replicas)
		e2eutil.WaitForNReadyNodesWithOptions(GinkgoTB(), ctx, hcClient, 1, hc.Spec.Platform.Type, fmt.Sprintf("for NodePool %s", autoscalingNP.Name), npLabelSelector)
	})
}

// AutoscalingBalancingTest tests that autoscaling balances workload across multiple NodePools.
// It configures the HostedCluster with the Random expander so the cluster autoscaler
// distributes scale-up events across NodePools instead of favoring one.
func AutoscalingBalancingTest(getTestCtx internal.TestContextGetter) {
	It("should balance pods across multiple autoscaling NodePools", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()

		e2eutil.GinkgoAtLeast(e2eutil.Version420)

		hc := testCtx.GetHostedCluster()
		hcClient := testCtx.GetHostedClusterClient()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		// Configure autoscaler with Random expander for balanced distribution.
		// The default least-waste expander favors a single NodePool.
		balancingLabel := "e2e-balance-ignore"
		originalHC := hc.DeepCopy()
		hc.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
			Expanders: []hyperv1.ExpanderString{
				hyperv1.RandomExpander,
			},
			BalancingIgnoredLabels: []string{
				balancingLabel,
			},
			MaxFreeDifferenceRatioPercent: ptr.To[int32](70),
		}
		err := testCtx.MgmtClient.Patch(ctx, hc, crclient.MergeFrom(originalHC))
		Expect(err).NotTo(HaveOccurred(), "failed to configure autoscaler on HostedCluster")
		GinkgoWriter.Println("Configured HostedCluster autoscaling with Random expander")

		DeferCleanup(func() {
			latest := &hyperv1.HostedCluster{}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), latest)).To(Succeed(),
				"cleanup: failed to get HostedCluster for autoscaling config reset")
			patch := crclient.MergeFrom(latest.DeepCopy())
			latest.Spec.Autoscaling = hyperv1.ClusterAutoscaling{}
			Expect(testCtx.MgmtClient.Patch(ctx, latest, patch)).To(Succeed(),
				"cleanup: failed to reset autoscaler config on HostedCluster")
		})

		// Wait for autoscaler deployment to pick up the new config
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "autoscaler deployment to have balancing config",
			func(ctx context.Context) (*appsv1.Deployment, error) {
				dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Namespace: cpNamespace, Name: "cluster-autoscaler",
				}}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(dep), dep)
				return dep, err
			},
			[]e2eutil.Predicate[*appsv1.Deployment]{func(dep *appsv1.Deployment) (bool, string, error) {
				for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
					if strings.Contains(arg, balancingLabel) {
						return dep.Status.ReadyReplicas > 0, fmt.Sprintf("ready replicas: %d", dep.Status.ReadyReplicas), nil
					}
				}
				return false, "balancing-ignore-label not found in autoscaler args", nil
			}},
			e2eutil.WithInterval(10*time.Second),
			e2eutil.WithTimeout(5*time.Minute),
		)

		// Find the default NodePool to copy platform config
		defaultNP := getDefaultNodePool(ctx, testCtx.MgmtClient, hc)
		Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

		// Create two autoscaling NodePools with distinct labels for the
		// balancing-ignored-labels config and a shared label for the workload nodeSelector.
		sharedLabel := map[string]string{"e2e-autoscaling-test": "balance"}
		np1Labels := map[string]string{
			"e2e-autoscaling-test": "balance",
			balancingLabel:         "np1",
		}
		np2Labels := map[string]string{
			"e2e-autoscaling-test": "balance",
			balancingLabel:         "np2",
		}

		autoscalingNP1 := buildAutoscalingNodePool(defaultNP, 1, 3, np1Labels)
		err = testCtx.MgmtClient.Create(ctx, autoscalingNP1)
		Expect(err).NotTo(HaveOccurred(), "failed to create first autoscaling NodePool")
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP1)
		})

		autoscalingNP2 := buildAutoscalingNodePool(defaultNP, 1, 3, np2Labels)
		err = testCtx.MgmtClient.Create(ctx, autoscalingNP2)
		Expect(err).NotTo(HaveOccurred(), "failed to create second autoscaling NodePool")
		DeferCleanup(func() {
			cleanupNodePool(ctx, testCtx.MgmtClient, autoscalingNP2)
		})

		np1LabelSelector := e2eutil.WithClientOptions(crclient.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: autoscalingNP1.Name}),
		})
		np2LabelSelector := e2eutil.WithClientOptions(crclient.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: autoscalingNP2.Name}),
		})

		// Wait for initial nodes (1 per NodePool at min replicas)
		nodes := e2eutil.WaitForNReadyNodesWithOptions(GinkgoTB(), ctx, hcClient, 1, hc.Spec.Platform.Type, "for NP1", np1LabelSelector)
		e2eutil.WaitForNReadyNodesWithOptions(GinkgoTB(), ctx, hcClient, 1, hc.Spec.Platform.Type, "for NP2", np2LabelSelector)

		// Get node capacity for workload sizing
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		bytes, ok := memCapacity.AsInt64()
		Expect(ok).To(BeTrue(), "memory capacity should be convertible to int64")

		// Create workload targeting the autoscaling NodePools via the shared label.
		workloadMemRequest := *resource.NewQuantity(bytes/2, resource.BinarySI)
		workload := newAutoscalingWorkload(4, workloadMemRequest, sharedLabel)
		err = hcClient.Create(ctx, workload)
		Expect(err).NotTo(HaveOccurred(), "failed to create workload")
		DeferCleanup(func() {
			cleanupWorkload(ctx, hcClient, workload)
		})

		// Wait for total 4 nodes across both NPs, then verify balanced distribution
		Eventually(func() (bool, error) {
			if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalingNP1), autoscalingNP1); err != nil {
				return false, err
			}
			if err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalingNP2), autoscalingNP2); err != nil {
				return false, err
			}

			total := autoscalingNP1.Status.Replicas + autoscalingNP2.Status.Replicas
			if total < 4 {
				return false, nil
			}
			return autoscalingNP1.Status.Replicas >= 1 && autoscalingNP2.Status.Replicas >= 1, nil
		}).WithTimeout(30*time.Minute).
			WithPolling(30*time.Second).
			Should(BeTrue(), "NodePools should have balanced distribution")
	})
}

// Helper functions

// getDefaultNodePool finds an existing NodePool for the hosted cluster to copy platform config
func getDefaultNodePool(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) *hyperv1.NodePool {
	GinkgoHelper()

	npList := &hyperv1.NodePoolList{}
	err := client.List(ctx, npList, crclient.InNamespace(hc.Namespace))
	Expect(err).NotTo(HaveOccurred(), "failed to list NodePools")
	Expect(npList.Items).NotTo(BeEmpty(), "should have at least one NodePool")

	// Find a NodePool for this HostedCluster
	for i := range npList.Items {
		if npList.Items[i].Spec.ClusterName == hc.Name {
			return &npList.Items[i]
		}
	}

	return nil
}

// buildAutoscalingNodePool creates a new NodePool with autoscaling enabled based on a template.
// nodeLabels are applied to the NodePool's nodes so workloads can target them with a nodeSelector.
func buildAutoscalingNodePool(template *hyperv1.NodePool, min, max int32, nodeLabels map[string]string) *hyperv1.NodePool {
	GinkgoHelper()

	name := e2eutil.SimpleNameGenerator.GenerateName(template.Spec.ClusterName + "-auto-")
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

	name := e2eutil.SimpleNameGenerator.GenerateName("autoscaling-workload-")
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

	err := client.Delete(ctx, np)
	if err != nil && !apierrors.IsNotFound(err) {
		GinkgoWriter.Printf("Warning: failed to delete NodePool %s: %v\n", np.Name, err)
	} else if err == nil {
		GinkgoWriter.Printf("Deleted NodePool %s\n", np.Name)
	}
}

// cleanupWorkload deletes a Job workload if it exists
func cleanupWorkload(ctx context.Context, client crclient.Client, job *batchv1.Job) {
	GinkgoHelper()

	cascadeDelete := metav1.DeletePropagationForeground
	err := client.Delete(ctx, job, &crclient.DeleteOptions{
		PropagationPolicy: &cascadeDelete,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		GinkgoWriter.Printf("Warning: failed to delete workload %s: %v\n", job.Name, err)
	} else if err == nil {
		GinkgoWriter.Printf("Deleted workload %s\n", job.Name)
	}
}

// RegisterNodePoolAutoscalingTests registers all autoscaling test cases
func RegisterNodePoolAutoscalingTests(getTestCtx internal.TestContextGetter) {
	AutoscalingScaleUpDownTest(getTestCtx)
	AutoscalingBalancingTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolAutoscaling] NodePool Autoscaling", Label("lifecycle", "nodepool-autoscaling"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodePoolAutoscalingTests(func() *internal.TestContext { return testCtx })
})
