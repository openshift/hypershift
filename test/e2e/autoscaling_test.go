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

	clusterOpts.NodePoolReplicas = 1
	var additionalNP *hyperv1.NodePool

	clusterOpts.BeforeApply = func(obj crclient.Object) {
		if nodepool, ok := obj.(*hyperv1.NodePool); ok {
			nodepool.Spec.NodeLabels = map[string]string{
				"custom.ignore.label": "test1",
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
				additionalNP.Spec.Replicas = ptr.To[int32](1)
			}
		}
	}
	clusterOpts.Annotations = append(clusterOpts.Annotations, fmt.Sprintf("%s=%s", hyperv1.ClusterAutoscalerImage, "quay.io/elmiko/cluster-autoscaler:pr8266"))

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Get the newly created NodePool
		nodepools := &hyperv1.NodePoolList{}
		if err := mgtClient.List(ctx, nodepools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
			t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
		}
		if len(nodepools.Items) != 1 {
			t.Fatalf("expected exactly one nodepool, got %d", len(nodepools.Items))
		}
		defaultNodePool := &nodepools.Items[0]

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
		numNodes := clusterOpts.NodePoolReplicas * 2
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

		// Enable autoscaling.
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(defaultNodePool), defaultNodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		min := clusterOpts.NodePoolReplicas
		max := min + 2
		originalDefault := defaultNodePool.DeepCopy()
		defaultNodePool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: min,
			Max: max,
		}
		defaultNodePool.Spec.Replicas = nil

		// Enable autoscaling for additional nodepool
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(additionalNodePool), additionalNodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get additional nodepool")
		originalAdditional := additionalNodePool.DeepCopy()
		additionalNodePool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: min,
			Max: max,
		}
		additionalNodePool.Spec.Replicas = nil

		// update NodePools
		err = mgtClient.Patch(ctx, defaultNodePool, crclient.MergeFrom(originalDefault))
		g.Expect(err).NotTo(HaveOccurred(), "failed to update default NodePool")
		err = mgtClient.Patch(ctx, additionalNodePool, crclient.MergeFrom(originalAdditional))
		g.Expect(err).NotTo(HaveOccurred(), "failed to update additional NodePool")

		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get latest HostedCluster")
		// Enable HostedCluster downscaling, set expanders and ignore labels
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
			MaxNodesTotal:                 ptr.To[int32](4),
			MaxFreeDifferenceRatioPercent: ptr.To[int32](50),
		}
		// update HostedCluster
		err = mgtClient.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster")

		t.Logf("Enabled autoscaling for both nodepools. Default: min=%v, max=%v, Additional: min=%v, max=%v", min, max, min, max)

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
		expectNodes := min*2 + 2 // default nodepool(min + 1) + additional nodepool(min + 1)
		workload := newWorkLoad(expectNodes, workloadMemRequest, "", globalOpts.LatestReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Created workload. Node: %s, memcapacity: %s, workload memory request: %s", nodes[0].Name, memCapacity.String(), workloadMemRequest.String())

		// Wait for one more node.
		// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
		t.Logf("Waiting for %d nodes to become ready...", expectNodes)
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, expectNodes, hostedCluster.Spec.Platform.Type)
		t.Logf("Successfully reached %d nodes", expectNodes)
		// Check load balancing between nodepools
		e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("both nodepools (%s and %s) to have 2 replicas each", defaultNodePool.Name, additionalNodePool.Name), func(ctx context.Context) ([]*hyperv1.NodePool, error) {
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
			if nps[0].Status.Replicas != 2 || nps[1].Status.Replicas != 2 {
				return false, fmt.Sprintf("nodepools replicas are %d and %d, want both 2", nps[0].Status.Replicas, nps[1].Status.Replicas), nil
			}
			return true, fmt.Sprintf("nodepools balanced - %s: %d, %s: %d", nps[0].Name, nps[0].Status.Replicas, nps[1].Name, nps[1].Status.Replicas), nil
		}}, nil, e2eutil.WithInterval(30*time.Second), e2eutil.WithTimeout(10*time.Minute))

		// Verify cluster autoscaler deployment arguments
		autoscalerDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-autoscaler",
				Namespace: hostedCluster.Namespace + "-" + hostedCluster.Name,
			},
		}
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(autoscalerDeployment), autoscalerDeployment)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get autoscaler deployment")
		args := autoscalerDeployment.Spec.Template.Spec.Containers[0].Args
		g.Expect(args).To(ContainElement("--scale-down-delay-after-add=300s"))
		g.Expect(args).To(ContainElement("--scale-down-unneeded-time=600s"))
		g.Expect(args).To(ContainElement("--scale-down-utilization-threshold=0.50"))
		g.Expect(args).To(ContainElement("--balancing-ignore-label=custom.ignore.label"))
		g.Expect(args).To(ContainElement("--max-free-difference-ratio=0.50"))

		// Delete workload.
		cascadeDelete := metav1.DeletePropagationForeground
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Deleted workload")

		// Wait for one less node.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "autoscaling", globalOpts.ServiceAccountSigningKey)

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
