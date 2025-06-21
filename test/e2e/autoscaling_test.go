//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAutoscaling(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	e2eutil.AtLeast(t, e2eutil.Version420)
	clusterOpts := globalOpts.DefaultClusterOptions(t)

	clusterOpts.BeforeApply = func(obj crclient.Object) {
		if nodepool, ok := obj.(*hyperv1.NodePool); ok {
			nodepool.Spec.NodeLabels = map[string]string{
				"custom.ignore.label": "test1",
			}
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Get the newly created NodePool
		nodepools := &hyperv1.NodePoolList{}
		if err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
			t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
		}
		if len(nodepools.Items) != 1 {
			t.Fatalf("expected exactly one nodepool, got %d", len(nodepools.Items))
		}
		nodepool := &nodepools.Items[0]

		// Perform some very basic assertions about the guest cluster
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
		numNodes := clusterOpts.NodePoolReplicas
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Enable autoscaling.
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

		min := numNodes
		max := min + 1
		original := nodepool.DeepCopy()
		nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: min,
			Max: max,
		}
		nodepool.Spec.Replicas = nil

		// Enable HostedCluster Autoscaling
		hostedCluster.Spec.Autoscaling = hyperv1.ClusterAutoscaling{
			ScaleDown: &hyperv1.ScaleDownConfig{
				Enabled:                 "Enabled",
				DelayAfterAddSeconds:    ptr.To[int32](300),
				UnneededDurationSeconds: ptr.To[int32](600),
				UtilizationThreshold:    ptr.To[string]("0.5"),
			},
			Expanders: []hyperv1.ExpanderString{
				hyperv1.LeastWasteExpander,
				hyperv1.PriorityExpander,
				hyperv1.RandomExpander,
			},
			BalancingIgnoredLabels: []string{
				"custom.ignore.label",
			},
		}

		// update NodePool
		err = mgtClient.Patch(ctx, nodepool, crclient.MergeFrom(original))
		g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool")

		// update HostedCluster
		err = mgtClient.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster")

		t.Logf("Enabled autoscaling. Namespace: %s, name: %s, min: %v, max: %v", nodepool.Namespace, nodepool.Name, min, max)

		// check NodePool autoscalingEnabled condition.
		e2eutil.EventuallyObject(t, ctx, "nodepool autoscaling to be enabled", func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
			return nodepool, err
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
		t.Logf("Created workload. Node: %s, memcapacity: %s, workload memory request: %s", nodes[0].Name, memCapacity.String(), workloadMemRequest.String())

		// Wait for one more node.
		numNodes = numNodes + 1
		t.Logf("Waiting for %d nodes to become ready...", numNodes)
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
		t.Logf("Successfully reached %d nodes", numNodes)

		// Verify cluster autoscaler deployment arguments
		autoscalerDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-autoscaler",
				Namespace: hostedCluster.Namespace + "-" + hostedCluster.Name,
			},
		}
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalerDeployment), autoscalerDeployment)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		args := autoscalerDeployment.Spec.Template.Spec.Containers[0].Args
		g.Expect(args).To(ContainElement("--scale-down-delay-after-add=300s"))
		g.Expect(args).To(ContainElement("--scale-down-unneeded-time=600s"))
		g.Expect(args).To(ContainElement("--scale-down-utilization-threshold=0.5"))
		g.Expect(args).To(ContainElement("--expander=least-waste,priority,random"))
		g.Expect(args).To(ContainElement("--balancing-ignore-label=custom.ignore.label"))

		// Delete workload.
		cascadeDelete := metav1.DeletePropagationForeground
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Deleted workload")

		// Wait for one less node.
		numNodes = numNodes - 1
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Test disabling scale down.
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get latest HostedCluster")
		hostedCluster.Spec.Autoscaling.ScaleDown.Enabled = "Disabled"
		err = mgtClient.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster")

		// Generate workload again.
		workload = newWorkLoad(max, workloadMemRequest, "", globalOpts.LatestReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())

		// Wait for one more node.
		numNodes = numNodes + 1
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Delete workload.
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())

		// Verify node count remains unchanged (because scale down is disabled).
		time.Sleep(30 * time.Second)
		currentNodes := &corev1.NodeList{}
		err = guestClient.List(ctx, currentNodes)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(int32(len(currentNodes.Items))).To(Equal(numNodes))

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "autoscaling", globalOpts.ServiceAccountSigningKey)

}

func newWorkLoad(njobs int32, memoryRequest resource.Quantity, nodeSelector, image string) *batchv1.Job {
	allowPrivilegeEscalation := false
	runAsNonRoot := true
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
