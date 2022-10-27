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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func testSetNodePoolAutoRepair(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer cancel()

		// List NodePools (should exists only one and without replicas)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestCluster.Name + "-" + "test-autorepair",
				Namespace: guestCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeReplace,
					AutoRepair:  true,
				},
				ClusterName: guestCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					Image: guestCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: guestCluster.Spec.Platform.Type,
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

			// Update NodePool
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			np := nodePool.DeepCopy()
			nodePool.Spec.Replicas = &twoReplicas
			nodePool.Spec.Management.AutoRepair = true
			if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update NodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
			g.Expect(err).NotTo(HaveOccurred(), "failed to Update existant NodePool")
		}

		numZones := int32(len(clusterOpts.AWSPlatform.Zones))
		if numZones <= 1 {
			clusterOpts.NodePoolReplicas = 2
		} else if numZones == 2 {
			clusterOpts.NodePoolReplicas = 1
		} else {
			clusterOpts.NodePoolReplicas = 1
		}
		numNodes := clusterOpts.NodePoolReplicas * numZones

		// Ensure we don't have the initial NodePool with replicas over 0
		if int32(*originalNP.Spec.Replicas) > zeroReplicas {
			// Wait until nodes gets created
			t.Logf("Waiting for Nodes %d\n", numNodes)
			_ = e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&originalNP), &originalNP)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			original := originalNP.DeepCopy()
			originalNP.Spec.Replicas = &zeroReplicas

			// Update NodePool
			if err := mgmtClient.Patch(ctx, &originalNP, crclient.MergeFrom(original)); err != nil {
				t.Fatalf("failed to update originalNP %s with Autorepair function: %v", originalNP.Name, err)
			}
			g.Expect(err).NotTo(HaveOccurred(), "failed to Update existant NodePool")
		}

		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

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
			nodes := e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)
			for _, node := range nodes {
				if node.Name == nodeToReplace {
					return false, nil
				}
			}
			return true, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")

		// Test Finished. Scalling down the NodePool to void waste resources
		err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
		np := nodePool.DeepCopy()
		nodePool.Spec.Replicas = &zeroReplicas
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to downscale nodePool %s: %v", nodePool.Name, err)
		}
	}
}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
