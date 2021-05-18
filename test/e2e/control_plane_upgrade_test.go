// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
)

// ControlPlaneUpgradeOptions are the raw user input used to construct the test input.
type ControlPlaneUpgradeOptions struct {
	AWSCredentialsFile string
	PullSecretFile     string
	FromReleaseImage   string
	ToReleaseImage     string
	ArtifactDir        string
}

func NewControlPlaneUpgradeOptions(globalOptions *GlobalTestOptions) ControlPlaneUpgradeOptions {
	return ControlPlaneUpgradeOptions{
		AWSCredentialsFile: globalOptions.AWSCredentialsFile,
		PullSecretFile:     globalOptions.PullSecretFile,
		FromReleaseImage:   globalOptions.PreviousReleaseImage,
		ToReleaseImage:     globalOptions.LatestReleaseImage,
		ArtifactDir:        globalOptions.ArtifactDir,
	}
}

func TestControlPlaneUpgrade(t *testing.T) {
	if GlobalOptions.IsRunningInCI {
		t.Skipf("upgrade test is not yet enabled in CI")
	}
	if !GlobalOptions.UpgradeTestsEnabled {
		t.Skipf("upgrade tests aren't enabled")
	}

	ctx, cancel := context.WithCancel(GlobalTestContext)
	defer cancel()

	opts := NewControlPlaneUpgradeOptions(GlobalOptions)
	t.Logf("Testing upgrade from %s to %s", opts.FromReleaseImage, opts.ToReleaseImage)

	g := NewWithT(t)

	client, err := crclient.New(ctrl.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kube client")

	// Create a namespace in which to place hostedclusters
	namespace := GenerateNamespace(t, ctx, client, "e2e-clusters-")

	// Define the cluster we'll be testing
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      "example",
		},
	}

	// Clean up the namespace after the test
	defer func() {
		DestroyCluster(t, context.Background(), &cmdcluster.DestroyOptions{
			Namespace:          hostedCluster.Namespace,
			Name:               hostedCluster.Name,
			Region:             GlobalOptions.Region,
			AWSCredentialsFile: opts.AWSCredentialsFile,
			EC2Client:          GlobalOptions.EC2Client,
			Route53Client:      GlobalOptions.Route53Client,
			ELBClient:          GlobalOptions.ELBClient,
			IAMClient:          GlobalOptions.IAMClient,
			ClusterGracePeriod: 15 * time.Minute,
		}, opts.ArtifactDir)
		DeleteNamespace(t, context.Background(), client, namespace.Name)
	}()

	// Create the cluster
	createClusterOpts := cmdcluster.Options{
		Namespace:          hostedCluster.Namespace,
		Name:               hostedCluster.Name,
		ReleaseImage:       opts.FromReleaseImage,
		PullSecretFile:     opts.PullSecretFile,
		AWSCredentialsFile: opts.AWSCredentialsFile,
		Region:             GlobalOptions.Region,
		EC2Client:          GlobalOptions.EC2Client,
		Route53Client:      GlobalOptions.Route53Client,
		ELBClient:          GlobalOptions.ELBClient,
		IAMClient:          GlobalOptions.IAMClient,
		// TODO: generate a key on the fly
		SSHKeyFile:       "",
		NodePoolReplicas: 0,
		InstanceType:     "m4.large",
	}
	err = cmdcluster.CreateCluster(ctx, createClusterOpts)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")

	// Get the newly created cluster
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	t.Logf("Created hostedcluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)

	// Wait for the cluster to be accessible
	WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for the first rollout to be complete
	t.Logf("Waiting for initial cluster rollout")
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 4*time.Minute)
		WaitForImageRollout(t, timeoutCtx, client, hostedCluster, opts.FromReleaseImage)
	}

	// Update the cluster image
	t.Logf("Updating cluster image")
	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	hostedCluster.Spec.Release.Image = opts.ToReleaseImage
	err = client.Update(ctx, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

	// Wait for the new rollout to be complete
	t.Logf("Waiting for updated cluster rollout")
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 4*time.Minute)
		WaitForImageRollout(t, timeoutCtx, client, hostedCluster, opts.ToReleaseImage)
	}
}
