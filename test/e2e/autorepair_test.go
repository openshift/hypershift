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
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestAutoRepair(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	numZones := int32(len(clusterOpts.AWSPlatform.Zones))
	if numZones <= 1 {
		clusterOpts.NodePoolReplicas = 3
	} else if numZones == 2 {
		clusterOpts.NodePoolReplicas = 2
	} else {
		clusterOpts.NodePoolReplicas = 1
	}
	clusterOpts.AutoRepair = true

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Perform some very basic assertions about the guest cluster
	guestClient := e2eutil.WaitForGuestClient(t, ctx, client, hostedCluster)
	// TODO (alberto): have ability to label and get Nodes by NodePool. NodePool.Status.Nodes?
	numNodes := clusterOpts.NodePoolReplicas * numZones
	nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be reported complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, ctx, client, guestClient, hostedCluster, globalOpts.LatestReleaseImage)

	// Terminate one of the machines belonging to the cluster
	nodeToReplace := nodes[0].Name
	awsSpec := nodes[0].Spec.ProviderID
	g.Expect(len(awsSpec)).NotTo(BeZero())
	instanceID := awsSpec[strings.LastIndex(awsSpec, "/")+1:]
	t.Logf("Terminating AWS instance: %s", instanceID)
	ec2client := ec2Client(clusterOpts.AWSPlatform.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)
	_, err = ec2client.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to terminate AWS instance")

	// Wait for nodes to be ready again, without the node that was terminated
	t.Logf("Waiting for %d available nodes without %s", numNodes, nodeToReplace)
	err = wait.PollUntil(30*time.Second, func() (done bool, err error) {
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)
		for _, node := range nodes {
			if node.Name == nodeToReplace {
				return false, nil
			}
		}
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
