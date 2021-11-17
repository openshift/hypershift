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

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions()
	clusterOpts.NodePoolReplicas = 3
	clusterOpts.AutoRepair = true

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, clusterOpts, globalOpts.ArtifactDir)

	// Get the newly created nodepool
	nodepool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Name,
		},
	}
	err := client.Get(testContext, crclient.ObjectKeyFromObject(nodepool), nodepool)
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
	ec2client := ec2Client(clusterOpts.AWSPlatform.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)
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

	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair")
	awsConfig := awsutil.NewConfig(awsCredsFile, region)
	return ec2.New(awsSession, awsConfig)
}
