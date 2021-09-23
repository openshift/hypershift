//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
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

	client := e2eutil.GetClientOrDie()

	// Create a namespace in which to place hostedclusters
	namespace := e2eutil.GenerateNamespace(t, testContext, client, "e2e-clusters-")
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
		// TODO: Figure out why this is slow
		//e2eutil.DumpGuestCluster(context.Background(), client, hostedCluster, globalOpts.ArtifactDir)
		e2eutil.DumpAndDestroyHostedCluster(t, context.Background(), hostedCluster, globalOpts.AWSCredentialsFile, globalOpts.Region, globalOpts.BaseDomain, globalOpts.ArtifactDir)
		e2eutil.DeleteNamespace(t, context.Background(), client, namespace.Name)
	}()

	// Create the cluster
	createClusterOpts := cmdcluster.Options{
		Namespace:          hostedCluster.Namespace,
		Name:               hostedCluster.Name,
		InfraID:            hostedCluster.Name,
		ReleaseImage:       globalOpts.LatestReleaseImage,
		PullSecretFile:     globalOpts.PullSecretFile,
		AWSCredentialsFile: globalOpts.AWSCredentialsFile,
		Region:             globalOpts.Region,
		// TODO: generate a key on the fly
		SSHKeyFile:                "",
		NodePoolReplicas:          2,
		InstanceType:              "m4.large",
		BaseDomain:                globalOpts.BaseDomain,
		NetworkType:               string(hyperv1.OpenShiftSDN),
		RootVolumeSize:            64,
		RootVolumeType:            "gp2",
		ControlPlaneOperatorImage: globalOpts.ControlPlaneOperatorImage,
		AdditionalTags:            globalOpts.AdditionalTags,
	}
	t.Logf("Creating a new cluster. Options: %v", createClusterOpts)
	err := cmdcluster.CreateCluster(testContext, createClusterOpts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

	// Get the newly created cluster
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	t.Logf("Found the new hostedcluster. Namespace: %s, name: %s", hostedCluster.Namespace, name)

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err = client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
	t.Logf("Created nodepool. Namespace: %s, name: %s", nodepool.Namespace, nodepool.Name)

	// Perform some very basic assertions about the guest cluster
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
	nodes := e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	// Enable autoscaling.
	err = client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
	var max int32 = 3

	// This Deployment have replicas=2 with
	// anti-affinity rules resulting in scheduling constraints
	// that prevent the cluster from ever scaling back down to 1:
	// aws-ebs-csi-driver-controller
	var min int32 = 2
	nodepool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{
		Min: min,
		Max: max,
	}
	nodepool.Spec.NodeCount = nil
	err = client.Update(testContext, nodepool)
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
	err = guestClient.Create(testContext, workload)
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Created workload. Node: %s, memcapacity: %s", nodes[0].Name, memCapacity.String())

	// Wait for 3 nodes.
	// TODO (alberto): have ability for NodePool to label Nodes and let workload target specific Nodes.
	_ = e2eutil.WaitForNReadyNodes(t, testContext, guestClient, max)

	// Delete workload.
	cascadeDelete := metav1.DeletePropagationForeground
	err = guestClient.Delete(testContext, workload, &crclient.DeleteOptions{
		PropagationPolicy: &cascadeDelete,
	})
	g.Expect(err).NotTo(HaveOccurred())
	t.Logf("Deleted workload")

	// Wait for exactly 1 node.
	_ = e2eutil.WaitForNReadyNodes(t, testContext, guestClient, min)
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
