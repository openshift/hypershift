//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NodePoolAutoRepairTest struct {
	DummyInfraSetup
	ctx context.Context

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         e2eutil.PlatformAgnosticOptions
}

func NewNodePoolAutoRepairTest(ctx context.Context, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolAutoRepairTest {
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
	ec2client := ec2Client(ar.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, ar.clusterOpts.AWSPlatform.Region)
	_, err := ec2client.TerminateInstancesWithContext(ar.ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to terminate AWS instance")

	numNodes := *nodePool.Spec.Replicas

	// Wait for nodes to be ready again, without the node that was terminated
	e2eutil.EventuallyObjects(t, ar.ctx, fmt.Sprintf("%d available nodes without %s", numNodes, nodeToReplace),
		func(ctx context.Context) ([]*corev1.Node, error) {
			list := &corev1.NodeList{}
			err = ar.hostedClusterClient.List(ctx, list, crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: nodePool.Name})})
			nodes := make([]*corev1.Node, len(list.Items))
			for i := range list.Items {
				nodes = append(nodes, &list.Items[i])
			}
			return nodes, err
		},
		[]e2eutil.Predicate[[]*corev1.Node]{
			// we want the number of nodes to heal back up to the specified number of replicas
			func(nodes []*corev1.Node) (done bool, reasons string, err error) {
				return len(nodes) == int(numNodes), fmt.Sprintf("expected %d nodes, got %d", numNodes, len(nodes)), nil
			},
			// we don't want the replaced node to exist
			func(nodes []*corev1.Node) (done bool, reasons string, err error) {
				for _, node := range nodes {
					if node.Name == nodeToReplace {
						return false, fmt.Sprintf("node %s not yet replaced", nodeToReplace), nil
					}
				}
				return true, fmt.Sprintf("node %s replaced", nodeToReplace), nil
			},
		},
		[]e2eutil.Predicate[*corev1.Node]{
			// all nodes need to be ready
			e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
				Type:   string(corev1.NodeReady),
				Status: metav1.ConditionTrue,
			}),
		},
	)

}

func ec2Client(awsCredsFile, region string) *ec2.EC2 {
	awsSession := awsutil.NewSession("e2e-autorepair", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.New(awsSession, awsConfig)
}
