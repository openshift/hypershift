//go:build e2e
// +build e2e

package e2e

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

func TestUpgradeControlPlane(t *testing.T) {
	//if !globalOpts.Enabled {
	//	t.Skipf("upgrade test is disabled")
	//}

	t.Parallel()
	g := NewWithT(t)

	client := e2eutil.GetClientOrDie()

	t.Logf("Starting control plane upgrade test. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

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
		ReleaseImage:       globalOpts.PreviousReleaseImage,
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
	err := cmdcluster.CreateCluster(testContext, createClusterOpts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

	// Get the newly created cluster
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	t.Logf("Created hostedcluster. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err = client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the first rollout to be complete
	t.Logf("Waiting for initial cluster rollout. Image: %s", globalOpts.PreviousReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.PreviousReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	// Update the cluster image
	t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	hostedCluster.Spec.Release.Image = globalOpts.LatestReleaseImage
	err = client.Update(testContext, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

	// Wait for the new rollout to be complete
	t.Logf("waiting for updated cluster image rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
}
