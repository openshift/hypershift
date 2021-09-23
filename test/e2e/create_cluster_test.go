//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
)

// TestCreateCluster implements a test that mimics the operation described in the
// HyperShift quick start (creating a basic guest cluster).
//
// This test is meant to provide a first, fast signal to detect regression; it
// is recommended to use it as a PR blocker test.
func TestCreateCluster(t *testing.T) {
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
	t.Logf("Found the new hostedcluster. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)

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

	// Wait for nodes to report ready
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

}
