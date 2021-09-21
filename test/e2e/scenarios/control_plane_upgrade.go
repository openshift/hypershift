// +build e2e

package scenarios

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
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
	CPOImage           string
}

func TestUpgradeControlPlane(ctx context.Context, o TestUpgradeControlPlaneOptions) func(t *testing.T) {
	return func(t *testing.T) {
		//if !o.Enabled {
		//	t.Skipf("upgrade test is disabled")
		//}

		t.Parallel()
		g := NewWithT(t)

		client := e2eutil.GetClientOrDie()

		t.Logf("Starting control plane upgrade test. FromImage: %s, toImage: %s", o.FromReleaseImage, o.ToReleaseImage)

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
			e2eutil.DumpAndDestroyHostedCluster(t, context.Background(), hostedCluster, o.AWSCredentialsFile, o.AWSRegion, o.BaseDomain, o.ArtifactDir)
			e2eutil.DeleteNamespace(t, context.Background(), client, namespace.Name)
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
			SSHKeyFile:                "",
			NodePoolReplicas:          2,
			InstanceType:              "m4.large",
			BaseDomain:                o.BaseDomain,
			NetworkType:               string(hyperv1.OpenShiftSDN),
			RootVolumeSize:            64,
			RootVolumeType:            "gp2",
			ControlPlaneOperatorImage: o.CPOImage,
		}
		err := cmdcluster.CreateCluster(ctx, createClusterOpts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		// Get the newly created cluster
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		t.Logf("Created hostedcluster. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)

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
		t.Logf("Waiting for guest client to become available")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, *nodepool.Spec.NodeCount)

		// Wait for the first rollout to be complete
		t.Logf("Waiting for initial cluster rollout. Image: %s", o.FromReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.FromReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// Update the cluster image
		t.Logf("Updating cluster image. Image: %s", o.ToReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		hostedCluster.Spec.Release.Image = o.ToReleaseImage
		err = client.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

		// Wait for the new rollout to be complete
		t.Logf("waiting for updated cluster image rollout", "image", o.ToReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.ToReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	}
}
