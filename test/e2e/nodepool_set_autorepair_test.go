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
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testSetNodePoolAutoRepair(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePools")

		// Check nodes and Nodepool replicas
		numZones := int32(len(clusterOpts.AWSPlatform.Zones))
		if numZones <= 1 {
			clusterOpts.NodePoolReplicas = 2
		} else if numZones == 2 {
			clusterOpts.NodePoolReplicas = 1
		} else {
			clusterOpts.NodePoolReplicas = 1
		}
		numNodes := clusterOpts.NodePoolReplicas * numZones

		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)

		for _, nodePool := range nodePools.Items {
			t.Logf("Checking availble Nodes at Region %s in Nodepool: %s", guestCluster.Spec.Platform.AWS.CloudProviderConfig.Zone, nodePool.Name)
			g.Expect(&nodePool.Status.Replicas).To(Equal(nodePool.Spec.Replicas))
			t.Logf("Checking AutoRepair function it's propertly set: %v", nodePool.Spec.Management.AutoRepair)
			if !nodePool.Spec.Management.AutoRepair {
				np := nodePool.DeepCopy()
				nodePool.Spec.Management.AutoRepair = true
				if err := mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
					t.Fatalf("failed to update nodePool %s with Autorepair function: %v", nodePool.Name, err)
				}
			}
		}

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
			nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)
			for _, node := range nodes {
				if node.Name == nodeToReplace {
					return false, nil
				}
			}
			return true, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")

		// Disabling Autorepair function
		for _, nodePool := range nodePools.Items {
			np := nodePool.DeepCopy()
			nodePool.Spec.Management.AutoRepair = false
			if err := mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
		}
	}
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
