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
			e2eutil.DumpGuestCluster(context.Background(), client, hostedCluster, o.ArtifactDir)
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
		}
		err := cmdcluster.CreateCluster(ctx, createClusterOpts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		// Get the newly created cluster
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		t.Logf("Created hostedcluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)

		// Wait for the cluster to be accessible
		e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)

		// Wait for the first rollout to be complete
		t.Logf("Waiting for initial cluster rollout")
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.FromReleaseImage)

		// Update the cluster image
		t.Logf("Updating cluster image")
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		hostedCluster.Spec.Release.Image = o.ToReleaseImage
		err = client.Update(ctx, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

		// Wait for the new rollout to be complete
		t.Logf("Waiting for updated cluster rollout")
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.ToReleaseImage)
	}
}
