//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type NodePoolDay2TagsTest struct {
	DummyInfraSetup

	ctx           context.Context
	mgmtClient    crclient.Client
	hostedCluster *hyperv1.HostedCluster
	clusterOpts   e2eutil.PlatformAgnosticOptions
}

func NewNodePoolDay2TagsTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolDay2TagsTest {
	return &NodePoolDay2TagsTest{
		ctx:           ctx,
		hostedCluster: hostedCluster,
		mgmtClient:    mgmtClient,
		clusterOpts:   clusterOpts,
	}
}

func (ar *NodePoolDay2TagsTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
}

func (ar *NodePoolDay2TagsTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ar.hostedCluster.Name + "-" + "test-day2-tags",
			Namespace: ar.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (ar *NodePoolDay2TagsTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	day2TagKey := "test-day2-tag"
	day2TagValue := "test-day2-value"

	err := e2eutil.UpdateObject(t, ar.ctx, ar.mgmtClient, &nodePool, func(nodePool *hyperv1.NodePool) {
		nodePool.Spec.Platform.AWS.ResourceTags = append(nodePool.Spec.Platform.AWS.ResourceTags, hyperv1.AWSResourceTag{
			Key:   day2TagKey,
			Value: day2TagValue,
		})
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to update nodePool tags")

	ec2client := ec2Client(ar.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, ar.clusterOpts.AWSPlatform.Region)
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(ar.hostedCluster.Namespace, ar.hostedCluster.Name)

	g.Eventually(func(g Gomega) {
		awsMachines := &capiaws.AWSMachineList{}
		err = ar.mgmtClient.List(ar.ctx, awsMachines, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels{
			capiv1.MachineDeploymentNameLabel: nodePool.Name,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list AWSMachines for node pool")

		for _, awsMachine := range awsMachines.Items {
			g.Expect(awsMachine.Spec.AdditionalTags).To(HaveKeyWithValue(day2TagKey, day2TagValue))

			instanceID := awsMachines.Items[0].Spec.InstanceID
			// Fetch the EC2 instance to verify the tag
			instance, err := ec2client.DescribeInstancesWithContext(ar.ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []*string{instanceID},
			})
			g.Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance")

			g.Expect(instance.Reservations).NotTo(BeEmpty(), "expected at least one reservation")
			g.Expect(instance.Reservations[0].Instances).NotTo(BeEmpty(), "expected at least one instance")
			g.Expect(instance.Reservations[0].Instances[0].Tags).To(ContainElement(&ec2.Tag{
				Key:   aws.String(day2TagKey),
				Value: aws.String(day2TagValue),
			}))
		}
	}).WithContext(ar.ctx).WithTimeout(time.Minute * 2).WithPolling(time.Second).Should(Succeed())

	// Ensure the machine deployment generation is not updated after the tag change.
	// This is to ensure that the tag change does not trigger a rolling update.
	// If the generation is updated, it indicates that the tag change triggered a rolling update
	// which is not an expected behavior.
	// The generation should only be updated when the node pool spec changes in a way that requires a rolling update
	// such as changing the instance type.
	md := &capiv1.MachineDeployment{}
	err = ar.mgmtClient.Get(ar.ctx, crclient.ObjectKey{
		Name:      nodePool.Name,
		Namespace: controlPlaneNamespace,
	}, md)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get machine deployment for node pool")

	g.Expect(md.Generation).To(BeEquivalentTo(1), "machine deployment generation should not change after day 2 tag update")
}
