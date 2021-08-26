// +build e2e

package scenarios

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type TestUpgradeControlPlaneOptions struct {
	AWSCredentialsFile string
	AWSRegion          string
	BaseDomain         string
	PullSecretFile     string
	FromReleaseImage   string
	ToReleaseImage     string
	ArtifactDir        string
	Enabled            bool
}

func TestUpgradeControlPlane(ctx context.Context, o TestUpgradeControlPlaneOptions) func(t *testing.T) {
	return func(t *testing.T) {
		if !o.Enabled {
			t.Skipf("upgrade test is disabled")
		}

		g := NewWithT(t)

		client := e2eutil.GetClientOrDie()

		log.Info("starting control plane upgrade test", "fromImage", o.FromReleaseImage, "toImage", o.ToReleaseImage)

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
			// TODO: Figure out why this is slow
			//e2eutil.DumpGuestCluster(context.Background(), client, hostedCluster, o.ArtifactDir)
			e2eutil.DumpAndDestroyHostedCluster(context.Background(), hostedCluster, o.AWSCredentialsFile, o.AWSRegion, o.BaseDomain, o.ArtifactDir)
			e2eutil.DeleteNamespace(context.Background(), client, namespace.Name)
		}()

		// Create the cluster
		createClusterOpts := cmdcluster.Options{
			Namespace:          hostedCluster.Namespace,
			Name:               hostedCluster.Name,
			InfraID:            hostedCluster.Name,
			ReleaseImage:       o.FromReleaseImage,
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
		err := cmdcluster.CreateCluster(ctx, createClusterOpts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		// Get the newly created cluster
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		log.Info("created hostedcluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)

		// Get the newly created nodepool
		nodepool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hostedCluster.Namespace,
				Name:      hostedCluster.Name,
			},
		}
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

		// Sanity check the cluster by waiting for the nodes to report ready
		log.Info("waiting for guest client to become available")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, *nodepool.Spec.NodeCount)

		// Wait for the first rollout to be complete
		log.Info("waiting for initial cluster rollout", "image", o.FromReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.FromReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// Update the cluster image
		log.Info("updating cluster image", "image", o.ToReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		hostedCluster.Spec.Release.Image = o.ToReleaseImage
		err = client.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

		// Wait for the new rollout to be complete
		log.Info("waiting for updated cluster image rollout", "image", o.ToReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.ToReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// TODO: This can be removed once the autoscaling scenario can be run in parallel
		// Enable autoscaling.
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
		var max int32 = 3

		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, *nodepool.Spec.NodeCount)

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
		workload := newWorkLoad(max, workloadMemRequest, "", o.ToReleaseImage)
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
