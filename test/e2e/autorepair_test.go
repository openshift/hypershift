//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAutoRepair(t *testing.T) {
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
		NodePoolReplicas:          3,
		InstanceType:              "m4.large",
		BaseDomain:                globalOpts.BaseDomain,
		NetworkType:               string(hyperv1.OpenShiftSDN),
		AutoRepair:                true,
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
	t.Logf("Found the new hostedcluster: %s", crclient.ObjectKeyFromObject(hostedCluster))

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err = client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
	t.Logf("Created nodepool: %s", crclient.ObjectKeyFromObject(nodepool))

	// Perform some very basic assertions about the guest cluster
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)
	// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
	nodes := e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, hostedCluster, globalOpts.LatestReleaseImage)

	// Terminate one of the machines belonging to the cluster
	nodeToReplace := nodes[0].Name
	awsSpec := nodes[0].Spec.ProviderID
	g.Expect(len(awsSpec)).NotTo(BeZero())
	instanceID := awsSpec[strings.LastIndex(awsSpec, "/")+1:]
	t.Logf("Terminating AWS instance: %s", instanceID)
	ec2client := ec2Client(globalOpts.AWSCredentialsFile, globalOpts.Region)
	_, err = ec2client.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to terminate AWS instance")

	// Wait for nodes to be ready again, without the node that was terminated
	t.Logf("Waiting for %d available nodes without %s", *nodepool.Spec.NodeCount, nodeToReplace)
	err = wait.PollUntil(30*time.Second, func() (done bool, err error) {
		nodes := e2eutil.WaitForNReadyNodes(t, testContext, guestClient, *nodepool.Spec.NodeCount)
		for _, node := range nodes {
			if node.Name == nodeToReplace {
				return false, nil
			}
		}
		return true, nil
	}, testContext.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair")
	awsConfig := awsutil.NewConfig(awsCredsFile, region)
	return ec2.New(awsSession, awsConfig)
}
