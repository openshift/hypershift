//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NodePoolImageTypeTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         e2eutil.PlatformAgnosticOptions
}

func NewNodePoolImageTypeTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolImageTypeTest {
	return &NodePoolImageTypeTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
		mgmtClient:          mgmtClient,
	}
}

func (it *NodePoolImageTypeTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test is only supported for AWS platform")
	}
	if e2eutil.IsLessThan(e2eutil.Version419) {
		t.Skip("test only supported from version 4.19")
	}
	t.Log("Starting test NodePoolImageTypeTest")
}

func (it *NodePoolImageTypeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      it.hostedCluster.Name + "-" + "test-imagetype",
			Namespace: it.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.AWS.InstanceType = "m5.metal"
	nodePool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows

	return nodePool, nil
}

func (it *NodePoolImageTypeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)
	ctx := it.ctx

	// Wait for the ValidPlatformImage condition to be populated with a settled status.
	// The condition may be True (Windows AMI found) or False (AMI not available for region/version).
	// We should not proceed while the status is Unknown (controller still discovering).
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("wait for nodepool %s/%s to have ValidPlatformImageType condition with settled status", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			np := &hyperv1.NodePool{}
			err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(np *hyperv1.NodePool) (bool, string, error) {
				// Find the ValidPlatformImageType condition
				for _, condition := range np.Status.Conditions {
					if condition.Type == hyperv1.NodePoolValidPlatformImageType {
						// Check if status is settled (True or False, not Unknown)
						if condition.Status == corev1.ConditionTrue || condition.Status == corev1.ConditionFalse {
							return true, fmt.Sprintf("condition settled: %s=%s, message: %s",
								condition.Type, condition.Status, condition.Message), nil
						}
						// Condition exists but status is still Unknown
						return false, fmt.Sprintf("condition exists but status is %s (waiting for settled state True or False)",
							condition.Status), nil
					}
				}
				// Condition not found yet
				return false, "ValidPlatformImageType condition not found", nil
			},
		},
		e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
	)

	// Validate that the ValidPlatformImageType condition reflects the Windows AMI.
	err := it.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
	validImageCondition := hostedcluster.FindNodePoolStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
	if validImageCondition == nil {
		t.Fatalf("ValidPlatformImageType condition not found")
	}

	switch validImageCondition.Status {
	case corev1.ConditionTrue:
		if !strings.Contains(strings.ToLower(validImageCondition.Message), "windows") {
			t.Fatalf("Windows ImageType should show Windows AMI info in condition message, but got: %s", validImageCondition.Message)
		}
		t.Logf("ValidPlatformImageType condition confirmed Windows AMI: %s", validImageCondition.Message)
	case corev1.ConditionFalse:
		if strings.Contains(strings.ToLower(validImageCondition.Message), "couldn't discover a windows ami") {
			t.Skipf("Windows AMI not available for this region/version: %s", validImageCondition.Message)
		}
		t.Fatalf("unexpected validation failure for Windows ImageType: %s", validImageCondition.Message)
	default:
		t.Fatalf("ValidPlatformImageType condition has unexpected status %s", validImageCondition.Status)
	}

	// Extract the expected Windows LI AMI ID from the condition message.
	// Message format: "Bootstrap Windows AMI is \"ami-0abcdef1234567890\""
	expectedAMI := extractAMIFromMessage(validImageCondition.Message)
	if expectedAMI == "" {
		t.Fatalf("failed to extract AMI ID from condition message: %s", validImageCondition.Message)
	}
	t.Logf("Expected Windows LI AMI: %s", expectedAMI)

	// Verify that nodes provisioned by the Windows LI AMI are running the expected RHCOS-based OS.
	// The Windows License Included (WinLI) AMI is a RHCOS image with Windows licensing metadata,
	// so nodes should report linux as their operating system and Red Hat Enterprise Linux CoreOS
	// in their OS image string.
	g.Expect(nodes).NotTo(BeEmpty(), "expected at least one ready node for the Windows ImageType nodepool")
	for _, node := range nodes {
		t.Logf("Checking node %s: OperatingSystem=%s, OSImage=%s",
			node.Name, node.Status.NodeInfo.OperatingSystem, node.Status.NodeInfo.OSImage)
		g.Expect(node.Status.NodeInfo.OperatingSystem).To(Equal("linux"),
			"Windows LI node %s should report linux as OperatingSystem", node.Name)
		g.Expect(strings.ToLower(node.Status.NodeInfo.OSImage)).To(ContainSubstring("red hat enterprise linux coreos"),
			"Windows LI node %s should be running RHCOS", node.Name)
	}

	// Verify that the EC2 instances are actually using the Windows LI AMI.
	// This is the critical validation that ensures customers will be compliant with AWS Windows LI terms.
	ec2client := ec2Client(it.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, it.clusterOpts.AWSPlatform.Region)
	for _, node := range nodes {
		// Check if context has been cancelled
		if ctx.Err() != nil {
			t.Fatalf("context cancelled while validating EC2 instances: %v", ctx.Err())
		}

		providerID := node.Spec.ProviderID
		g.Expect(providerID).NotTo(BeEmpty(), "node %s should have a providerID", node.Name)

		// Extract instance ID from providerID (format: aws:///us-east-1a/i-1234567890abcdef0)
		parts := strings.Split(providerID, "/")
		if len(parts) == 0 {
			t.Fatalf("node %s has invalid providerID format: %s", node.Name, providerID)
		}
		instanceID := parts[len(parts)-1]
		if instanceID == "" {
			t.Fatalf("node %s has empty instance ID in providerID: %s", node.Name, providerID)
		}
		t.Logf("Verifying EC2 instance %s for node %s", instanceID, node.Name)

		result, err := ec2client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []*string{aws.String(instanceID)},
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance %s", instanceID)
		g.Expect(result.Reservations).NotTo(BeEmpty(), "no reservations found for instance %s", instanceID)
		g.Expect(result.Reservations[0].Instances).NotTo(BeEmpty(), "no instances found in reservation for instance %s", instanceID)

		instance := result.Reservations[0].Instances[0]
		actualAMI := aws.StringValue(instance.ImageId)
		t.Logf("Node %s is running on EC2 instance %s with AMI %s", node.Name, instanceID, actualAMI)

		// This is the critical validation: ensure the EC2 instance is using the Windows LI AMI.
		// If this check fails, it means the NodePool controller found the Windows AMI but didn't
		// actually apply it to the EC2 instances, which would result in compliance violations.
		g.Expect(actualAMI).To(Equal(expectedAMI),
			"EC2 instance %s for node %s should be using Windows LI AMI %s, but is using %s",
			instanceID, node.Name, expectedAMI, actualAMI)
	}

	t.Log("NodePoolImageTypeTest passed - Windows LI AMI validated at all layers (NodePool condition, Node OS, EC2 AMI)")
}

// extractAMIFromMessage extracts the AMI ID from a NodePool condition message.
// Expected message format: "Bootstrap Windows AMI is \"ami-0abcdef1234567890\""
func extractAMIFromMessage(message string) string {
	// Match AMI ID pattern: ami- followed by exactly 8 hex chars (old format) or 17 hex chars (new format)
	// Old format: ami-12345678
	// New format: ami-0abcdef1234567890
	re := regexp.MustCompile(`ami-[0-9a-f]{8}(?:[0-9a-f]{9})?`)
	matches := re.FindString(message)
	return matches
}
