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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NodePoolAutoRepairTest struct {
	ctx context.Context

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         core.CreateOptions
}

func NewNodePoolAutoRepairTest(ctx context.Context, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts core.CreateOptions) *NodePoolAutoRepairTest {
	return &NodePoolAutoRepairTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
	}
}

func (ar *NodePoolAutoRepairTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
}

func (ar *NodePoolAutoRepairTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ar.hostedCluster.Name + "-" + "test-autorepair",
			Namespace: ar.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Management.AutoRepair = true

	return nodePool, nil
}

func (ar *NodePoolAutoRepairTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	// Terminate one of the machines belonging to the cluster
	t.Log("Terminating AWS Instance with a autorepair NodePool")
	nodeToReplace := nodes[0].Name
	awsSpec := nodes[0].Spec.ProviderID
	g.Expect(len(awsSpec)).NotTo(BeZero())
	instanceID := awsSpec[strings.LastIndex(awsSpec, "/")+1:]
	t.Logf("Terminating AWS instance: %s", instanceID)
	ec2client := ec2Client(ar.clusterOpts.AWSPlatform.AWSCredentialsFile, ar.clusterOpts.AWSPlatform.Region)
	_, err := ec2client.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to terminate AWS instance")

	numNodes := *nodePool.Spec.Replicas

	// Wait for nodes to be ready again, without the node that was terminated
	t.Logf("Waiting for %d available nodes without %s", numNodes, nodeToReplace)
	err = wait.PollUntil(30*time.Second, func() (done bool, err error) {
		nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ar.ctx, ar.hostedClusterClient, numNodes, ar.hostedCluster.Spec.Platform.Type, nodePool.Name)
		for _, node := range nodes {
			if node.Name == nodeToReplace {
				return false, nil
			}
		}
		return true, nil
	}, ar.ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for new node to become available")

}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
