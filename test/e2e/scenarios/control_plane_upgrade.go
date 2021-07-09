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
			NetworkType:      string(hyperv1.OpenShiftSDN),
		}
		err := cmdcluster.CreateCluster(ctx, createClusterOpts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

		// Get the newly created cluster
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
		log.Info("created hostedcluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)

		// Wait for the first rollout to be complete
		log.Info("waiting for initial cluster rollout", "image", o.FromReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, client, hostedCluster, o.FromReleaseImage)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		// Get the newly created nodepool
		nodepool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hostedCluster.Namespace,
				Name:      hostedCluster.Name,
			},
		}
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodepool), nodepool)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")

		// Ensure the cluster becomes solvent
		log.Info("waiting for updated cluster rollout", "version", hostedCluster.Status.Version.History[0].Version)
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
		e2eutil.WaitForReadyNodes(t, ctx, guestClient, nodepool)
		e2eutil.WaitForClusterOperators(t, ctx, guestClient, hostedCluster,
			e2eutil.OperatorIsReady(), e2eutil.OperatorAtVersion(hostedCluster.Status.Version.History[0].Version))

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

		// Ensure the cluster reaches the new version in a healthy state
		log.Info("waiting for updated cluster rollout", "version", hostedCluster.Status.Version.History[0].Version)
		e2eutil.WaitForReadyNodes(t, ctx, guestClient, nodepool)
		e2eutil.WaitForClusterOperators(t, ctx, guestClient, hostedCluster,
			e2eutil.OperatorIsReady(), e2eutil.OperatorAtVersion(hostedCluster.Status.Version.History[0].Version))
	}
}
