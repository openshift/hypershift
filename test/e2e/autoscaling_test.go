//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/podspec"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAutoscaling(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	clusterOpts.NodePoolReplicas = 1
	var additionalNP *hyperv1.NodePool

	clusterOpts.BeforeApply = func(obj crclient.Object) {
		if nodepool, ok := obj.(*hyperv1.NodePool); ok {
			nodepool.Spec.NodeLabels = map[string]string{
				"custom.ignore.label": "test1",
			}

			// Set instance type to m5.xlarge for autoscaling tests to increase node capacity
			if nodepool.Spec.Platform.AWS != nil {
				nodepool.Spec.Platform.AWS.InstanceType = "m5.xlarge"
			}

			if additionalNP == nil {
				additionalNP = &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nodepool.Name + "-additional",
						Namespace: nodepool.Namespace,
					},
				}
				nodepool.Spec.DeepCopyInto(&additionalNP.Spec)

				additionalNP.Spec.NodeLabels = map[string]string{
					"custom.ignore.label": "test2",
				}
				additionalNP.Spec.Replicas = nil
				additionalNP.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
					Min: ptr.To[int32](1),
					Max: 3,
				}

				// Also set m5.xlarge for the additional NodePool
				if additionalNP.Spec.Platform.AWS != nil {
					additionalNP.Spec.Platform.AWS.InstanceType = "m5.xlarge"
				}
			}
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("TestAutoscaling", testAutoscaling(ctx, mgtClient, hostedCluster, clusterOpts.NodePoolReplicas, clusterOpts.NodePoolReplicas+2))

		t.Run("TestAutoscalerRespectsNodePoolPause", testAutoscalerRespectsNodePoolPause(ctx, mgtClient, hostedCluster, clusterOpts.NodePoolReplicas, clusterOpts.NodePoolReplicas+2))

		t.Run("TestAutoscalingBalancing", testAutoscalingBalancing(ctx, mgtClient, hostedCluster, clusterOpts.NodePoolReplicas*2, additionalNP))
	}).WithAssetReader(content.ReadFile).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "autoscaling", globalOpts.ServiceAccountSigningKey)

}

func testAutoscaling(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, numNodes, max int32) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Get the newly created NodePool
		nodepool := getOnlyNodePool(t, ctx, mgtClient, hostedCluster.Namespace)

		// Perform some very basic assertions about the guest cluster
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Enable autoscaling.
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

		original := nodepool.DeepCopy()
		nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: ptr.To[int32](numNodes),
			Max: max,
		}
		nodepool.Spec.Replicas = nil
		err = mgtClient.Patch(ctx, nodepool, crclient.MergeFrom(original))
		g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool")
		t.Logf("Enabled autoscaling. Namespace: %s, name: %s, min: %v, max: %v", nodepool.Namespace, nodepool.Name, numNodes, max)

		// TODO (alberto): check autoscalingEnabled condition.

		// Generate workload.
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		g.Expect(memCapacity).ShouldNot(BeNil())
		g.Expect(memCapacity.String()).ShouldNot(BeEmpty())
		bytes, ok := memCapacity.AsInt64()
		g.Expect(ok).Should(BeTrue())

		// Enforce max nodes creation.
		// 50% - enough that the existing and new nodes will
		// be used, not enough to have more than 1 pod per
		// node.
		workloadMemRequest := resource.MustParse(fmt.Sprintf("%v", 0.5*float32(bytes)))
		workload := newWorkLoad(max, workloadMemRequest, "", globalOpts.LatestReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Created workload. Node: %s, memcapacity: %s", nodes[0].Name, memCapacity.String())
		defer func() {
			// Clean up workload if WaitForNReadyNodes fails
			cascadeDelete := metav1.DeletePropagationForeground
			// Ignore error, might be already deleted
			_ = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
				PropagationPolicy: &cascadeDelete,
			})
		}()

		// Wait for one more node.
		// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, max, hostedCluster.Spec.Platform.Type)

		// Delete workload.
		cascadeDelete := metav1.DeletePropagationForeground
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Deleted workload")

		// Wait for one less node.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
	}
}

// testAutoscalingBalancing tests the balancing scale-up functionality
// This test reuses the same HostedCluster created in TestAutoscaling
func testAutoscalingBalancing(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, numNodes int32, additionalNP *hyperv1.NodePool) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		t.Log("Starting balancing scale-up test")
		e2eutil.AtLeast(t, e2eutil.Version420)

		// Get the newly created NodePool
		defaultNodePool := getOnlyNodePool(t, ctx, mgtClient, hostedCluster.Namespace)

		// create additional NodePool
		if additionalNP != nil {
			additionalNP.Namespace = hostedCluster.Namespace
			additionalNP.Spec.ClusterName = hostedCluster.Name
			err := mgtClient.Create(ctx, additionalNP)
			g.Expect(err).NotTo(HaveOccurred(), "failed to create additional nodepool")
			t.Logf("Created additional nodepool: %s", additionalNP.Name)
		}
		// get additional NodePool
		additionalNodePool := &hyperv1.NodePool{}
		err := mgtClient.Get(ctx, crclient.ObjectKey{Name: additionalNP.Name, Namespace: hostedCluster.Namespace}, additionalNodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get additional nodepool")

		// Perform some very basic assertions about the guest cluster
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Enable HostedCluster downscaling, set expanders and ignore labels
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
				return err
			}
			hostedCluster.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				Expanders: []hyperv1.ExpanderString{
					hyperv1.RandomExpander,
				},
				ScaleDown: &hyperv1.ScaleDownConfig{
					DelayAfterAddSeconds:        ptr.To[int32](300),
					UnneededDurationSeconds:     ptr.To[int32](600),
					UtilizationThresholdPercent: ptr.To[int32](50),
				},
				BalancingIgnoredLabels: []string{
					"custom.ignore.label",
				},
				MaxNodesTotal:                 ptr.To[int32](6),
				MaxFreeDifferenceRatioPercent: ptr.To[int32](70),
			}
			return mgtClient.Update(ctx, hostedCluster)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster")

		// check NodePool autoscalingEnabled condition for both nodepools
		e2eutil.EventuallyObject(t, ctx, "default nodepool autoscaling to be enabled", func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNodePool), defaultNodePool)
			return defaultNodePool, err
		}, []e2eutil.Predicate[*hyperv1.NodePool]{func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
			for _, condition := range np.Status.Conditions {
				if condition.Type == hyperv1.NodePoolAutoscalingEnabledConditionType {
					return condition.Status == corev1.ConditionTrue,
						fmt.Sprintf("autoscaling condition status is %s", condition.Status),
						nil
				}
			}
			return false, "autoscaling condition not found", nil
		}}, e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute))

		e2eutil.EventuallyObject(t, ctx, "additional nodepool autoscaling to be enabled", func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(additionalNodePool), additionalNodePool)
			return additionalNodePool, err
		}, []e2eutil.Predicate[*hyperv1.NodePool]{func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
			for _, condition := range np.Status.Conditions {
				if condition.Type == hyperv1.NodePoolAutoscalingEnabledConditionType {
					return condition.Status == corev1.ConditionTrue,
						fmt.Sprintf("autoscaling condition status is %s", condition.Status),
						nil
				}
			}
			return false, "autoscaling condition not found", nil
		}}, e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute))

		// Wait for autoscaler deployment to have autoscaling settings and be ready
		// TODO (cewong): This should be reported in the HostedCluster as a condition
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		waitForAutoscalerDeploymentReady(t, ctx, mgtClient, controlPlaneNamespace, []string{"custom.ignore.label"})

		// Generate workload.
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		g.Expect(memCapacity).ShouldNot(BeNil())
		g.Expect(memCapacity.String()).ShouldNot(BeEmpty())
		bytes, ok := memCapacity.AsInt64()
		g.Expect(ok).Should(BeTrue())

		// Enforce max nodes creation.
		// 50% - enough that the existing and new nodes will
		// be used, not enough to have more than 1 pod per
		// node.
		workloadMemRequest := resource.MustParse(fmt.Sprintf("%v", 0.5*float32(bytes)))
		expectNodes := int32(6) // Target 6 nodes total for balancing test
		workload := newWorkLoad(6, workloadMemRequest, "", globalOpts.LatestReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Created workload. Node: %s, memcapacity: %s, workload memory request: %s", nodes[0].Name, memCapacity.String(), workloadMemRequest.String())

		// Wait for one more node.
		// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
		t.Logf("Waiting for %d nodes to become ready...", expectNodes)
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, expectNodes, hostedCluster.Spec.Platform.Type)
		t.Logf("Successfully reached %d nodes", expectNodes)
		// Check load balancing between nodepools
		e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("both nodepools (%s and %s) to have reasonable distribution totaling %d nodes", defaultNodePool.Name, additionalNodePool.Name, expectNodes), func(ctx context.Context) ([]*hyperv1.NodePool, error) {
			nodePools := []*hyperv1.NodePool{defaultNodePool, additionalNodePool}
			for _, np := range nodePools {
				if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(np), np); err != nil {
					return nil, err
				}
			}
			return nodePools, nil
		}, []e2eutil.Predicate[[]*hyperv1.NodePool]{func(nps []*hyperv1.NodePool) (bool, string, error) {
			if len(nps) != 2 {
				return false, fmt.Sprintf("expected 2 nodepools, got %d", len(nps)), nil
			}
			// Relaxing the check to allow reasonable distribution between nodepools, it's not deterministic which nodepool will get the nodes.
			// This supports 2+4, 3+3, 4+2 configurations (each nodepool must have at least 2 nodes).
			// With this we make sure no nodepool has ≤1 nodes and resolve flaky tests.
			totalReplicas := nps[0].Status.Replicas + nps[1].Status.Replicas
			if totalReplicas != 6 {
				return false, fmt.Sprintf("total replicas is %d, want 6", totalReplicas), nil
			}
			if nps[0].Status.Replicas <= 1 || nps[1].Status.Replicas <= 1 {
				return false, fmt.Sprintf("unbalanced: nodepool has ≤1 nodes (%d, %d)", nps[0].Status.Replicas, nps[1].Status.Replicas), nil
			}

			return true, fmt.Sprintf("nodepools balanced - %s: %d, %s: %d", nps[0].Name, nps[0].Status.Replicas, nps[1].Name, nps[1].Status.Replicas), nil
		}}, nil, e2eutil.WithInterval(30*time.Second), e2eutil.WithTimeout(10*time.Minute))
	}
}

func newWorkLoad(njobs int32, memoryRequest resource.Quantity, nodeSelector, image string) *batchv1.Job {
	allowPrivilegeEscalation := false
	runAsNonRoot := false
	runAsUser := int64(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "autoscaling-workload",
			Namespace: "default",
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Job",
			APIVersion: "batch/v1",
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "autoscaling-workload",
							Image: image,
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
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
								RunAsNonRoot: &runAsNonRoot,
								RunAsUser:    &runAsUser,
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicy("Never"),
				},
			},
			BackoffLimit: ptr.To[int32](4),
			Completions:  ptr.To[int32](njobs),
			Parallelism:  ptr.To[int32](njobs),
		},
	}
	if nodeSelector != "" {
		job.Spec.Template.Spec.NodeSelector = map[string]string{
			nodeSelector: "",
		}
	}
	return job
}

const (
	aggressiveDelayAfterAddSeconds    int32 = 30
	aggressiveUnneededDurationSeconds int32 = 60
	casScaleDownWaitDuration                = time.Duration(aggressiveUnneededDurationSeconds+aggressiveDelayAfterAddSeconds)*time.Second + 90*time.Second
	casLogPollInterval                      = 5 * time.Second
	casPausedLogMessage                     = "discovered a paused node group"
)

// getOnlyNodePool lists NodePools in the given namespace and asserts exactly one exists.
// It returns the single NodePool, failing the test if zero or more than one are found.
func getOnlyNodePool(t *testing.T, ctx context.Context, mgtClient crclient.Client, namespace string) *hyperv1.NodePool {
	t.Helper()
	g := NewWithT(t)
	nodepools := &hyperv1.NodePoolList{}
	err := mgtClient.List(ctx, nodepools, crclient.InNamespace(namespace))
	g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools")
	g.Expect(nodepools.Items).To(HaveLen(1), "expected exactly one nodepool")
	return &nodepools.Items[0]
}

// waitForAutoscalerDeploymentReady waits for the cluster-autoscaler deployment in the given
// control plane namespace to contain all requiredArgs and be ready.
func waitForAutoscalerDeploymentReady(t *testing.T, ctx context.Context, mgtClient crclient.Client, controlPlaneNamespace string, requiredArgs []string) {
	t.Helper()
	e2eutil.EventuallyObject(t, ctx, "autoscaler deployment to have required settings and be ready",
		func(ctx context.Context) (*appsv1.Deployment, error) {
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "cluster-autoscaler"}}
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(dep), dep)
			return dep, err
		},
		[]e2eutil.Predicate[*appsv1.Deployment]{
			func(dep *appsv1.Deployment) (done bool, reasons string, err error) {
				for _, required := range requiredArgs {
					found := false
					for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
						if strings.Contains(arg, required) {
							found = true
							break
						}
					}
					if !found {
						return false, fmt.Sprintf("autoscaler deployment missing %s arg", required), nil
					}
				}
				return podspec.IsDeploymentReady(ctx, dep), "autoscaler deployment not ready", nil
			},
		},
		e2eutil.WithInterval(10*time.Second),
		e2eutil.WithTimeout(5*time.Minute),
	)
}

// pollCASLogsForPausedNodeGroup polls the cluster-autoscaler pod logs for the
// "discovered a paused node group" message, which the CAS fix emits at klog.V(4)
// when it skips a paused MachineDeployment. Returns true if the message was found
// before the timeout, false otherwise.
func pollCASLogsForPausedNodeGroup(t *testing.T, ctx context.Context, controlPlaneNamespace string, timeout time.Duration) bool {
	t.Helper()

	cfg, err := e2eutil.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get management cluster config: %v", err)
	}
	kubeClient := kubeclient.NewForConfigOrDie(cfg)

	sinceTime := metav1.Now()
	t.Logf("Polling CAS logs for paused node group detection (timeout=%s)...", timeout)

	err = wait.PollUntilContextTimeout(ctx, casLogPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := kubeClient.CoreV1().Pods(controlPlaneNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=cluster-autoscaler",
		})
		if err != nil || len(pods.Items) == 0 {
			return false, nil
		}

		stream, err := kubeClient.CoreV1().Pods(controlPlaneNamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
			Container: "cluster-autoscaler",
			SinceTime: &sinceTime,
		}).Stream(ctx)
		if err != nil {
			return false, nil
		}
		defer stream.Close()

		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), casPausedLogMessage) {
				return true, nil
			}
		}
		return false, nil
	})

	if err != nil {
		t.Logf("Timeout waiting for CAS paused node group log — proceeding with replica verification")
		return false
	}
	t.Logf("CAS log confirms paused node group was detected and skipped")
	return true
}

// testAutoscalerRespectsNodePoolPause is a regression test for OCPBUGS-78152 / CNTRLPLANE-3040.
//
// It verifies that the Cluster Autoscaler does NOT decrement MachineDeployment.spec.replicas
// on paused MachineDeployments. Without the CAS fix, CAS would accumulate replica decrements
// while the MachineDeployment is paused (because machines are never deleted), causing
// catastrophic scale-down on unpause.
//
// Test flow:
// 1. Enable autoscaling on the NodePool (self-contained)
// 2. Configure aggressive CAS scale-down timers
// 3. Scale up the NodePool to max nodes with workload
// 4. Pause the NodePool (propagates cluster.x-k8s.io/paused to MachineDeployment)
// 5. Delete the workload so nodes become unneeded
// 6. Poll CAS logs for "discovered a paused node group" (or timeout)
// 7. Verify MachineDeployment.spec.replicas has NOT been decremented
// 8. Unpause and wait for clean convergence to numNodes
func testAutoscalerRespectsNodePoolPause(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, numNodes, max int32) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Get the NodePool.
		nodepool := getOnlyNodePool(t, ctx, mgtClient, hostedCluster.Namespace)

		// Step 1: Ensure autoscaling is enabled on the NodePool so this test is
		// self-contained and does not depend on a prior subtest.
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool); err != nil {
				return err
			}
			nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
				Min: ptr.To[int32](numNodes),
				Max: max,
			}
			nodepool.Spec.Replicas = nil
			return mgtClient.Update(ctx, nodepool)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to enable autoscaling on NodePool")
		t.Logf("Enabled autoscaling on NodePool %s (min=%d, max=%d)", nodepool.Name, numNodes, max)

		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
		t.Logf("Cluster has %d ready nodes", len(nodes))

		// Step 2: Configure aggressive CAS scale-down timers on the HostedCluster.
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
				return err
			}
			hostedCluster.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					DelayAfterAddSeconds:        ptr.To(aggressiveDelayAfterAddSeconds),
					UnneededDurationSeconds:     ptr.To(aggressiveUnneededDurationSeconds),
					UtilizationThresholdPercent: ptr.To[int32](50),
				},
			}
			return mgtClient.Update(ctx, hostedCluster)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to configure autoscaling on HostedCluster")
		t.Log("Configured aggressive CAS scale-down timers (unneeded=60s, delayAfterAdd=30s)")

		// Wait for autoscaler deployment to have the aggressive scale-down args and be ready.
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		waitForAutoscalerDeploymentReady(t, ctx, mgtClient, controlPlaneNamespace, []string{
			"--scale-down-unneeded-time=60s",
			"--scale-down-delay-after-add=30s",
		})
		t.Log("Autoscaler deployment is ready with aggressive scale-down settings")

		// Step 3: Generate workload to scale up to max nodes.
		memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
		g.Expect(memCapacity).ShouldNot(BeNil())
		g.Expect(memCapacity.String()).ShouldNot(BeEmpty())
		bytes, ok := memCapacity.AsInt64()
		g.Expect(ok).Should(BeTrue())

		// 50% - enough that the existing and new nodes will be used, not enough to have more than 1 pod per node.
		workloadMemRequest := resource.MustParse(fmt.Sprintf("%v", 0.5*float32(bytes)))
		workload := newWorkLoad(max, workloadMemRequest, "", globalOpts.LatestReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Created workload. Node: %s, memcapacity: %s", nodes[0].Name, memCapacity.String())
		defer func() {
			cascadeDelete := metav1.DeletePropagationForeground
			if err := guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
				PropagationPolicy: &cascadeDelete,
			}); err != nil {
				t.Logf("cleanup: failed to delete workload: %v", err)
			}
		}()

		// Wait for max nodes.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, max, hostedCluster.Spec.Platform.Type)
		t.Logf("Scaled up to %d nodes", max)

		// Step 4: Pause the NodePool. This propagates cluster.x-k8s.io/paused to the MachineDeployment.
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool); err != nil {
				return err
			}
			nodepool.Spec.PausedUntil = ptr.To("true")
			return mgtClient.Update(ctx, nodepool)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to pause NodePool")
		t.Logf("Paused NodePool %s", nodepool.Name)
		defer func() {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool); err != nil {
					return err
				}
				if nodepool.Spec.PausedUntil == nil {
					return nil
				}
				nodepool.Spec.PausedUntil = nil
				return mgtClient.Update(ctx, nodepool)
			}); err != nil {
				t.Logf("cleanup: failed to unpause NodePool %s: %v", nodepool.Name, err)
			}
		}()

		// Verify the MachineDeployment has the pause annotation.
		md := &capiv1.MachineDeployment{}
		e2eutil.EventuallyObject(t, ctx, "MachineDeployment to be paused",
			func(ctx context.Context) (*capiv1.MachineDeployment, error) {
				err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: nodepool.Name}, md)
				return md, err
			},
			[]e2eutil.Predicate[*capiv1.MachineDeployment]{
				func(md *capiv1.MachineDeployment) (done bool, reasons string, err error) {
					if md.Annotations[capiv1.PausedAnnotation] == "true" {
						return true, "MachineDeployment is paused", nil
					}
					return false, "MachineDeployment not yet paused", nil
				},
			},
			e2eutil.WithTimeout(2*time.Minute),
		)

		// Record the replica count before CAS has a chance to act.
		replicasBeforePause := *md.Spec.Replicas
		t.Logf("MachineDeployment is paused with %d replicas", replicasBeforePause)

		// Step 5: Delete workload so nodes become unneeded.
		cascadeDelete := metav1.DeletePropagationForeground
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Deleted workload")

		// Step 6: Poll CAS pod logs for evidence it evaluated the paused node group.
		// The CAS fix logs "discovered a paused node group" at klog.V(4) when it
		// skips a paused MachineDeployment. We poll for this line instead of blindly
		// sleeping, which is both faster and deterministic.
		pauseDetected := pollCASLogsForPausedNodeGroup(t, ctx, controlPlaneNamespace, casScaleDownWaitDuration)

		// Step 7: Verify MachineDeployment replicas have NOT been decremented.
		err = mgtClient.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: nodepool.Name}, md)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get MachineDeployment")

		replicasAfterWait := *md.Spec.Replicas
		if pauseDetected {
			t.Logf("CAS confirmed paused node group — replicas after CAS evaluation: %d (was %d before pause)", replicasAfterWait, replicasBeforePause)
		} else {
			t.Logf("CAS log not detected within timeout — replicas after wait: %d (was %d before pause)", replicasAfterWait, replicasBeforePause)
		}
		g.Expect(replicasAfterWait).To(Equal(replicasBeforePause),
			"MachineDeployment replicas should not change while paused — CAS should not decrement replicas on paused MachineDeployments (regression for OCPBUGS-78152)")

		// Step 8: Unpause the NodePool.
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool); err != nil {
				return err
			}
			nodepool.Spec.PausedUntil = nil
			return mgtClient.Update(ctx, nodepool)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to unpause NodePool")
		t.Logf("Unpaused NodePool %s", nodepool.Name)

		// Verify the MachineDeployment pause annotation is removed.
		e2eutil.EventuallyObject(t, ctx, "MachineDeployment to be unpaused",
			func(ctx context.Context) (*capiv1.MachineDeployment, error) {
				err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: nodepool.Name}, md)
				return md, err
			},
			[]e2eutil.Predicate[*capiv1.MachineDeployment]{
				func(md *capiv1.MachineDeployment) (done bool, reasons string, err error) {
					if md.Annotations[capiv1.PausedAnnotation] != "true" {
						return true, "MachineDeployment is unpaused", nil
					}
					return false, "MachineDeployment still paused", nil
				},
			},
			e2eutil.WithTimeout(2*time.Minute),
		)
		t.Log("MachineDeployment is unpaused")

		// After unpause, CAS may resume normal scale-down immediately since the
		// workload is gone and aggressive timers are satisfied. Verify the cluster
		// converges cleanly back to the autoscaling minimum.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
		t.Logf("Scaled down to %d nodes after unpause — clean state for next test", numNodes)
	}
}

func TestNodePoolAutoscalingScaleFromZero(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}

	// Get management client to check for scale-from-zero secret
	mgtClient, err := e2eutil.GetClient()
	if err != nil {
		t.Fatalf("failed to get management client: %v", err)
	}

	// Check if scale-from-zero is enabled by looking for the credentials secret
	// The instance type provider is enabled when this secret is set
	scaleFromZeroSecret := &corev1.Secret{}
	err = mgtClient.Get(testContext, crclient.ObjectKey{
		Namespace: "hypershift",
		Name:      "hypershift-operator-scale-from-zero-credentials",
	}, scaleFromZeroSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			t.Skip("test requires scale-from-zero to be enabled on the HyperShift Operator (secret hypershift-operator-scale-from-zero-credentials not found)")
		}
		t.Fatalf("failed to check for scale-from-zero secret: %v", err)
	}

	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 1

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("TestScaleFromZero", testScaleFromZero(ctx, mgtClient, hostedCluster))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "scale-from-zero", globalOpts.ServiceAccountSigningKey)
}

func testScaleFromZero(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Security context settings for workload pods
		allowPrivilegeEscalation := false
		runAsNonRoot := false
		runAsUser := int64(0)

		// Get guest client
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Get default NodePool to copy spec
		nodepools := &hyperv1.NodePoolList{}
		err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace))
		g.Expect(err).NotTo(HaveOccurred(), "failed to list nodepools")
		g.Expect(nodepools.Items).NotTo(BeEmpty(), "expected at least one nodepool")

		// Create NodePool with scale-from-zero autoscaling
		scaleFromZeroNP := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostedCluster.Name + "-scale-from-zero",
				Namespace: hostedCluster.Namespace,
			},
		}
		nodepools.Items[0].Spec.DeepCopyInto(&scaleFromZeroNP.Spec)
		scaleFromZeroNP.Spec.Replicas = nil // Must be nil when using autoscaling
		scaleFromZeroNP.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: ptr.To[int32](0), // Scale from zero
			Max: 2,
		}
		// Add unique labels to nodes from this NodePool so workload can target them
		scaleFromZeroNP.Spec.NodeLabels = map[string]string{
			"scale-from-zero-test": "true",
		}

		err = mgtClient.Create(ctx, scaleFromZeroNP)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create scale-from-zero nodepool")
		t.Logf("Created NodePool %s with autoscaling min=0, max=2", scaleFromZeroNP.Name)

		// Verify MachineDeployment has capacity annotations
		t.Log("Verifying MachineDeployment has capacity annotations")
		md := &capiv1.MachineDeployment{}
		e2eutil.EventuallyObject(t, ctx, "MachineDeployment to have capacity annotations",
			func(ctx context.Context) (*capiv1.MachineDeployment, error) {
				// MachineDeployment is in the hosted cluster namespace with same name as NodePool
				err := mgtClient.Get(ctx, crclient.ObjectKey{
					Namespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
					Name:      scaleFromZeroNP.Name,
				}, md)
				return md, err
			},
			[]e2eutil.Predicate[*capiv1.MachineDeployment]{
				func(md *capiv1.MachineDeployment) (done bool, reasons string, err error) {
					if _, ok := md.Annotations["machine.openshift.io/vCPU"]; !ok {
						return false, "missing vCPU annotation", nil
					}
					if _, ok := md.Annotations["machine.openshift.io/memoryMb"]; !ok {
						return false, "missing memoryMb annotation", nil
					}
					// GPU annotation is optional - only set when instance type has GPUs
					labels, ok := md.Annotations["capacity.cluster-autoscaler.kubernetes.io/labels"]
					if !ok {
						return false, "missing capacity labels annotation", nil
					}
					if !strings.Contains(labels, "kubernetes.io/arch=") {
						return false, "capacity labels missing architecture", nil
					}
					return true, "all capacity annotations present", nil
				},
			},
			e2eutil.WithTimeout(5*time.Minute),
		)
		gpuValue := md.Annotations["machine.openshift.io/GPU"]
		if gpuValue == "" {
			gpuValue = "none (non-GPU instance)"
		}
		t.Logf("MachineDeployment has capacity annotations: vCPU=%s, memoryMb=%s, GPU=%s, labels=%s",
			md.Annotations["machine.openshift.io/vCPU"],
			md.Annotations["machine.openshift.io/memoryMb"],
			gpuValue,
			md.Annotations["capacity.cluster-autoscaler.kubernetes.io/labels"])

		// Verify NodePool autoscaling is enabled
		e2eutil.EventuallyObject(t, ctx, "NodePool autoscaling to be enabled",
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), scaleFromZeroNP)
				return scaleFromZeroNP, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
					for _, condition := range np.Status.Conditions {
						if condition.Type == hyperv1.NodePoolAutoscalingEnabledConditionType {
							return condition.Status == corev1.ConditionTrue,
								fmt.Sprintf("autoscaling enabled status is %s", condition.Status),
								nil
						}
					}
					return false, "autoscaling enabled condition not found", nil
				},
			},
			e2eutil.WithTimeout(5*time.Minute),
		)

		// Verify NodePool starts with 0 replicas
		t.Log("Verifying NodePool starts with 0 replicas")
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), scaleFromZeroNP)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(scaleFromZeroNP.Status.Replicas).To(Equal(int32(0)), "NodePool should start with 0 replicas")
		t.Log("Confirmed NodePool has 0 replicas")

		// Create workload to trigger scale-up from 0
		t.Log("Creating workload to trigger scale-up from 0 nodes")
		workload := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scale-from-zero-workload",
				Namespace: "default",
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:    "workload",
								Image:   globalOpts.LatestReleaseImage,
								Command: []string{"sleep", "3600"},
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										"memory": resource.MustParse("1Gi"),
										"cpu":    resource.MustParse("500m"),
									},
								},
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &allowPrivilegeEscalation,
									Capabilities: &corev1.Capabilities{
										Drop: []corev1.Capability{
											"ALL",
										},
									},
									RunAsNonRoot: &runAsNonRoot,
									RunAsUser:    &runAsUser,
									SeccompProfile: &corev1.SeccompProfile{
										Type: corev1.SeccompProfileTypeRuntimeDefault,
									},
								},
							},
						},
						// Target only nodes from the scale-from-zero NodePool
						NodeSelector: map[string]string{
							"scale-from-zero-test": "true",
						},
						RestartPolicy: corev1.RestartPolicyNever,
					},
				},
				Completions: ptr.To[int32](2),
				Parallelism: ptr.To[int32](2),
			},
		}

		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create workload")
		t.Log("Created workload with 2 pods")

		defer func() {
			cascadeDelete := metav1.DeletePropagationForeground
			_ = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
				PropagationPolicy: &cascadeDelete,
			})
		}()

		// Wait for NodePool to scale from 0 to at least 1
		// Note: We request 2 pods but they may fit on a single node depending on instance capacity
		t.Log("Waiting for NodePool to scale from 0 to at least 1 node")
		e2eutil.EventuallyObject(t, ctx, "NodePool to scale from 0",
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(scaleFromZeroNP), scaleFromZeroNP)
				return scaleFromZeroNP, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				func(np *hyperv1.NodePool) (done bool, reasons string, err error) {
					if np.Status.Replicas > 0 {
						return true, fmt.Sprintf("NodePool scaled to %d replicas", np.Status.Replicas), nil
					}
					return false, fmt.Sprintf("NodePool has %d replicas, waiting for >0", np.Status.Replicas), nil
				},
			},
			e2eutil.WithInterval(10*time.Second),
			e2eutil.WithTimeout(15*time.Minute),
		)
		t.Logf("NodePool successfully scaled from 0 to %d replicas", scaleFromZeroNP.Status.Replicas)

		// Verify pods are scheduled and running
		t.Log("Verifying workload pods are scheduled and running")
		e2eutil.EventuallyObjects(t, ctx, "Pods to be scheduled and running",
			func(ctx context.Context) ([]*corev1.Pod, error) {
				pods := &corev1.PodList{}
				err := guestClient.List(ctx, pods,
					crclient.InNamespace("default"),
					crclient.MatchingLabels{"job-name": "scale-from-zero-workload"})
				items := make([]*corev1.Pod, len(pods.Items))
				for i := range pods.Items {
					items[i] = &pods.Items[i]
				}
				return items, err
			},
			[]e2eutil.Predicate[[]*corev1.Pod]{
				func(pods []*corev1.Pod) (done bool, reasons string, err error) {
					if len(pods) < 2 {
						return false, fmt.Sprintf("expected at least 2 pods, got %d", len(pods)), nil
					}
					return true, fmt.Sprintf("found %d pods (>= 2)", len(pods)), nil
				},
			},
			[]e2eutil.Predicate[*corev1.Pod]{
				e2eutil.ConditionPredicate[*corev1.Pod](e2eutil.Condition{
					Type:   string(corev1.PodScheduled),
					Status: metav1.ConditionTrue,
				}),
				e2eutil.Predicate[*corev1.Pod](func(pod *corev1.Pod) (done bool, reasons string, err error) {
					if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded {
						return true, fmt.Sprintf("pod %s is %s", pod.Name, pod.Status.Phase), nil
					}
					return false, fmt.Sprintf("pod %s is %s", pod.Name, pod.Status.Phase), nil
				}),
			},
			e2eutil.WithTimeout(20*time.Minute),
		)
		t.Log("Successfully verified scale-from-zero: workload pods are scheduled and running on scaled nodes")
	}
}
