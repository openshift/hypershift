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
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testNodePoolAutoRepair(parentCtx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions, testSigEnd chan<- bool) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer func() {
			t.Log("Test: NodePoolAutoRepair finished")
			cancel()
			testSigEnd <- true
		}()

		// List NodePools (should exists only one)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: hostedCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(BeEmpty())
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostedCluster.Name + "-" + "test-autorepair",
				Namespace: hostedCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeReplace,
					AutoRepair:  true,
				},
				ClusterName: hostedCluster.Name,
				Replicas:    &oneReplicas,
				Release: hyperv1.Release{
					Image: hostedCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: hostedCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}

		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
			err = nodePoolRecreate(t, ctx, nodePool, mgmtClient)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}
		defer nodePoolScaleDownToZero(ctx, mgmtClient, *nodePool, t)

		numNodes := int32(1)

		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		// Wait for the rollout to be reported complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedClusterClient, hostedCluster, globalOpts.LatestReleaseImage)

		// Terminate one of the machines belonging to the cluster
		t.Log("Terminating AWS Instance with a autorepare NodePool")
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
			nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
			for _, node := range nodes {
				if node.Name == nodeToReplace {
					return false, nil
				}
			}
			return true, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")
	}
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
