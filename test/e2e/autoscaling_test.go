//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
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
	// create one nodePool with 1 replica in each AZ
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	clusterOpts.AWSPlatform.Zones = zones
	clusterOpts.NodePoolReplicas = 1

	numNodePools := len(zones)

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
	validatePublicCluster(t, ctx, client, hostedCluster, &clusterOpts)

	// list the newly created NodePools
	nodepools := &hyperv1.NodePoolList{}
	if err := client.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}
	if len(nodepools.Items) != numNodePools {
		t.Fatalf("expected %d nodepool, got %d", numNodePools, len(nodepools.Items))
	}

	// Enable autoscaling.
	min := int32(1)
	max := int32(3)
	mutateFunc := func(nodepool *hyperv1.NodePool) {
		nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: min,
			Max: max,
		}
		nodepool.Spec.Replicas = nil
	}

	for _, nodepool := range nodepools.Items {
		err := e2eutil.UpdateObject(t, ctx, client, &nodepool, mutateFunc)
		g.Expect(err).ToNot(HaveOccurred(), "failed to update NodePool %s", nodepool.Name)
	}
	t.Logf("Enabled autoscaling for all nodePools in namespace: %s, min: %v, max: %v", hostedCluster.Namespace, min, max)

	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
	nodes := &corev1.NodeList{}
	err = guestClient.List(ctx, nodes)
	g.Expect(err).ToNot(HaveOccurred(), "failed to list nodes")
	g.Expect(nodes.Items).ToNot(BeEmpty())

	numNodes := len(nodes.Items)

	memCapacity := nodes.Items[0].Status.Allocatable[corev1.ResourceMemory]
	g.Expect(memCapacity).ShouldNot(BeNil())
	g.Expect(memCapacity.String()).ShouldNot(BeEmpty())
	bytes, ok := memCapacity.AsInt64()
	g.Expect(ok).Should(BeTrue())

	// 60% - enough that the existing and new nodes will
	// be used, not enough to have more than 1 pod per
	// node.
	workloadMemRequest := resource.MustParse(fmt.Sprintf("%v", 0.6*float32(bytes)))

	// force the cluster to double its size. the cluster autoscaler should
	// place 1 more node in each of the the nodepools created.
	jobReplicas := int32(numNodes * 2)
	workload := newWorkLoad(jobReplicas, workloadMemRequest, "", globalOpts.LatestReleaseImage)
	err = guestClient.Create(ctx, workload)
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Created workload with memcapacity: %s", memCapacity.String())

	// Validate nodepools are scaled out and balanced.
	// Each nodepool should have 1 more node.
	for _, nodepool := range nodepools.Items {
		expectedNodesNum := min + 1
		_ = e2eutil.WaitForNReadyNodesByNodePool(t, ctx, guestClient, expectedNodesNum, hostedCluster.Spec.Platform.Type, nodepool.Name)
	}

	// Delete workload.
	cascadeDelete := metav1.DeletePropagationForeground
	err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
		PropagationPolicy: &cascadeDelete,
	})
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Deleted workload")

	// Wait for the cluster to scale down again to the original nodes number.
	_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, int32(numNodes), hostedCluster.Spec.Platform.Type)
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
			BackoffLimit: pointer.Int32(4),
			Completions:  pointer.Int32(njobs),
			Parallelism:  pointer.Int32(njobs),
		},
	}
	if nodeSelector != "" {
		job.Spec.Template.Spec.NodeSelector = map[string]string{
			nodeSelector: "",
		}
	}
	return job
}
