//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAutoscaling(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)

	// Get the newly created NodePool
	nodepools := &hyperv1.NodePoolList{}
	if err := client.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}
	if len(nodepools.Items) != 1 {
		t.Fatalf("expected exactly one nodepool, got %d", len(nodepools.Items))
	}
	nodepool := &nodepools.Items[0]

	// Perform some very basic assertions about the guest cluster
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
	// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
	numNodes := clusterOpts.NodePoolReplicas
	nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, globalOpts.LatestReleaseImage)

	// Enable autoscaling.
	err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

	min := numNodes
	max := min + 1
	original := nodepool.DeepCopy()
	nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
		Min: min,
		Max: max,
	}
	nodepool.Spec.Replicas = nil
	err = client.Patch(ctx, nodepool, crclient.MergeFrom(original))
	g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool")
	t.Logf("Enabled autoscaling. Namespace: %s, name: %s, min: %v, max: %v", nodepool.Namespace, nodepool.Name, min, max)

	// TODO (alberto): check autoscalingEnabled condition.

	// Generate workload.
	memCapacity := nodes[0].Status.Allocatable[corev1.ResourceMemory]
	g.Expect(memCapacity).ShouldNot(BeNil())
	g.Expect(memCapacity.String()).ShouldNot(BeEmpty())
	bytes, ok := memCapacity.AsInt64()
	g.Expect(ok).Should(BeTrue())

	// Enforce max nodes creation.
	// 60% - enough that the existing and new nodes will
	// be used, not enough to have more than 1 pod per
	// node.
	workloadMemRequest := resource.MustParse(fmt.Sprintf("%v", 0.6*float32(bytes)))
	workload := newWorkLoad(max, workloadMemRequest, "", globalOpts.LatestReleaseImage)
	err = guestClient.Create(ctx, workload)
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Created workload. Node: %s, memcapacity: %s", nodes[0].Name, memCapacity.String())

	// Wait for one more node.
	// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
	numNodes = numNodes + 1
	_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

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
			BackoffLimit: pointer.Int32Ptr(4),
			Completions:  pointer.Int32Ptr(njobs),
			Parallelism:  pointer.Int32Ptr(njobs),
		},
	}
	if nodeSelector != "" {
		job.Spec.Template.Spec.NodeSelector = map[string]string{
			nodeSelector: "",
		}
	}
	return job
}
