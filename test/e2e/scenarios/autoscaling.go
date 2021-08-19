// +build e2e

package scenarios

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/alknopfler/hypershift/api/v1alpha1"
	cmdcluster "github.com/alknopfler/hypershift/cmd/cluster"
	e2eutil "github.com/alknopfler/hypershift/test/e2e/util"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type TestAutoscalingOptions struct {
	AWSCredentialsFile string
	AWSRegion          string
	PullSecretFile     string
	ReleaseImage       string
	ArtifactDir        string
	BaseDomain         string
}

func TestAutoscaling(ctx context.Context, o TestAutoscalingOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		client := e2eutil.GetClientOrDie()

		// Create a namespace in which to place hostedclusters
		namespace := e2eutil.GenerateNamespace(t, ctx, client, "e2e-clusters-")
		name := e2eutil.SimpleNameGenerator.GenerateName("example-")

		// Define the cluster we'll be testing
		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name,
				Name:      name,
			},
		}

		// Ensure we clean up after the test
		defer func() {
			e2eutil.DumpGuestCluster(context.Background(), client, hostedCluster, o.ArtifactDir)
			e2eutil.DumpAndDestroyHostedCluster(context.Background(), hostedCluster, o.AWSCredentialsFile, o.AWSRegion, o.BaseDomain, o.ArtifactDir)
			e2eutil.DeleteNamespace(context.Background(), client, namespace.Name)
		}()

		// Create the cluster
		createClusterOpts := cmdcluster.Options{
			Namespace:          hostedCluster.Namespace,
			Name:               hostedCluster.Name,
			InfraID:            hostedCluster.Name,
			ReleaseImage:       o.ReleaseImage,
			PullSecretFile:     o.PullSecretFile,
			AWSCredentialsFile: o.AWSCredentialsFile,
			Region:             o.AWSRegion,
			// TODO: generate a key on the fly
			SSHKeyFile:       "",
			NodePoolReplicas: 2,
			InstanceType:     "m4.large",
			BaseDomain:       o.BaseDomain,
			NetworkType:      string(hyperv1.OpenShiftSDN),
		}
		log.Info("Creating a new cluster", "options", createClusterOpts)
		err := cmdcluster.CreateCluster(ctx, createClusterOpts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		// Get the newly created cluster
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		log.Info("Found the new hostedcluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)

		// Get the newly created nodepool
		nodepool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hostedCluster.Namespace,
				Name:      hostedCluster.Name,
			},
		}
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		log.Info("Created nodepool", "namespace", nodepool.Namespace, "name", nodepool.Name)

		// Perform some very basic assertions about the guest cluster
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
		// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, *nodepool.Spec.NodeCount)

		// Wait for the rollout to be reported complete
		log.Info("waiting for cluster rollout", "image", o.ReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.ReleaseImage)

		// Enable autoscaling.
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		var max int32 = 3

		// These Deployments have replicas=2 with
		// anti-affinity rules resulting in scheduling constraints
		// that prevent the cluster from ever scaling back down to 1:
		// aws-ebs-csi-driver-controller
		// console
		// router-default
		// thanos-querier
		// prometheus-adapter
		var min int32 = 2
		nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
			Min: min,
			Max: max,
		}
		nodepool.Spec.NodeCount = nil
		err = client.Update(ctx, nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to update NodePool")
		log.Info("Enabled autoscaling",
			"namespace", nodepool.Namespace, "name", nodepool.Name, "min", min, "max", max)

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
		workload := newWorkLoad(max, workloadMemRequest, "", o.ReleaseImage)
		err = guestClient.Create(ctx, workload)
		g.Expect(err).NotTo(HaveOccurred())
		log.Info("Created workload", "node", nodes[0].Name, "memcapacity", memCapacity.String())

		// Wait for 3 nodes.
		// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, max)

		// Delete workload.
		cascadeDelete := metav1.DeletePropagationForeground
		err = guestClient.Delete(ctx, workload, &crclient.DeleteOptions{
			PropagationPolicy: &cascadeDelete,
		})
		g.Expect(err).NotTo(HaveOccurred())
		log.Info("Deleted workload")

		// Wait for exactly 1 node.
		_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, min)
	}
}

func newWorkLoad(njobs int32, memoryRequest resource.Quantity, nodeSelector, image string) *batchv1.Job {
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
