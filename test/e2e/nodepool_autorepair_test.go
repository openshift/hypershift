//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	if globalOpts.Platform != hyperv1.AWSPlatform && globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform AWS and Kubevirt")
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
	nodePool.Spec.NodeDrainTimeout = &metav1.Duration{
		Duration: 1 * time.Second,
	}

	return nodePool, nil
}

func (ar *NodePoolAutoRepairTest) awsMakeNodeUnhealthy(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) string {
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

	return nodeToReplace
}

func (ar *NodePoolAutoRepairTest) kubevirtMakeNodeUnhealthy(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) string {
	g := NewWithT(t)

	c, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")
	hcluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: ar.hostedCluster.Namespace,
		Name:      ar.hostedCluster.Name,
	}}
	err = c.Get(ar.ctx, client.ObjectKeyFromObject(hcluster), hcluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to retrieve hosted cluster")
	g.Expect(hcluster.Status.KubeConfig).NotTo(Equal(""), "failed to detect guest cluster kubeconfig")

	kubeconfigSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: hcluster.Namespace,
		Name:      hcluster.Status.KubeConfig.Name,
	}}

	c.Get(ar.ctx, client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to retrieve guest cluster kubeconfig secret")

	kubeconfigFile, err := os.CreateTemp(os.TempDir(), "kubeconfig-")
	g.Expect(err).NotTo(HaveOccurred(), "failed to create tempfile for kubeconfig")

	defer func() {
		kubeconfigFile.Close()
		os.Remove(kubeconfigFile.Name())
	}()
	_, err = kubeconfigFile.Write(kubeconfigSecret.Data["kubeconfig"])
	g.Expect(err).NotTo(HaveOccurred(), "failed to write kubeconfig data")

	// Terminate one of the machines belonging to the cluster
	t.Log("Terminating KubeVirt Instance with a autorepair NodePool")
	nodeToReplace := nodes[0].Name

	t.Logf("Killing KubeVirt node instance: %s", nodeToReplace)
	ocCommand, err := exec.LookPath("oc")
	g.Expect(err).NotTo(HaveOccurred(), "failed to find oc command tool")
	g.Expect(ocCommand).NotTo(Equal(""), "failed to find oc command tool")

	//oc --kubeconfig test-kubeconfig debug node/vossel1-q25s4  -- /bin/bash -c "echo 'systemctl status' | chroot /host"

	args := []string{
		"--kubeconfig",
		kubeconfigFile.Name(),
		"debug",
		"node/" + nodeToReplace,
		"--",
		"/bin/bash",
		"-c",
		"echo 'systemctl stop kubelet' | chroot /host",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ocCommand, args...)

	var outb bytes.Buffer
	var errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	// err is expected from cmd run. killing the kubelet kills the connection and returns non-zero exit.
	err = cmd.Run()
	t.Log(fmt.Sprintf("command oc %s: %v, %s, %s", strings.Join(args, " "), err, errb.String(), outb.String()))
	return nodeToReplace
}

func (ar *NodePoolAutoRepairTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	var nodeToReplace string

	g := NewWithT(t)

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		nodeToReplace = ar.awsMakeNodeUnhealthy(t, nodePool, nodes)
	case hyperv1.KubevirtPlatform:
		nodeToReplace = ar.kubevirtMakeNodeUnhealthy(t, nodePool, nodes)
	}
	numNodes := *nodePool.Spec.Replicas

	// Wait for nodes to be ready again, without the node that was terminated
	t.Logf("Waiting for %d available nodes without %s", numNodes, nodeToReplace)
	err := wait.PollUntil(30*time.Second, func() (done bool, err error) {
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
