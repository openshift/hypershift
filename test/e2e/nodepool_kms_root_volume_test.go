//go:build e2e
// +build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

type KMSRootVolumeTest struct {
	hostedCluster *hyperv1.HostedCluster
	clusterOpts   core.CreateOptions

	EncryptionKey string
}

func NewKMSRootVolumeTest(hostedCluster *hyperv1.HostedCluster, clusterOpts core.CreateOptions) *KMSRootVolumeTest {
	return &KMSRootVolumeTest{
		hostedCluster: hostedCluster,
		clusterOpts:   clusterOpts,
	}
}

func (k *KMSRootVolumeTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}

	t.Log("Starting test KMSRootVolumeTest")

	// find kms key ARN using alias
	kmsKeyArn, err := e2eutil.GetKMSKeyArn(k.clusterOpts.AWSPlatform.AWSCredentialsFile, k.clusterOpts.AWSPlatform.Region)
	if err != nil || kmsKeyArn == nil {
		t.Fatalf("failed to retrieve kms key arn")
	}

	k.EncryptionKey = *kmsKeyArn
	t.Logf("retrieved kms arn: %s", k.EncryptionKey)
}

func (k *KMSRootVolumeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kms-root-volume",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.AWS.RootVolume = &hyperv1.Volume{
		Size:          k.clusterOpts.AWSPlatform.RootVolumeSize,
		Type:          k.clusterOpts.AWSPlatform.RootVolumeType,
		EncryptionKey: k.EncryptionKey,
	}

	return nodePool, nil
}

func (k *KMSRootVolumeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	providerID := nodes[0].Spec.ProviderID
	g.Expect(providerID).NotTo(BeEmpty())

	instanceID := providerID[strings.LastIndex(providerID, "/")+1:]
	t.Logf("instanceID: %s", instanceID)

	ec2client := ec2Client(k.clusterOpts.AWSPlatform.AWSCredentialsFile, k.clusterOpts.AWSPlatform.Region)
	output, err := ec2client.DescribeVolumes(&ec2.DescribeVolumesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("attachment.instance-id"),
				Values: aws.StringSlice([]string{instanceID}),
			},
			{
				Name:   aws.String("encrypted"),
				Values: aws.StringSlice([]string{"true"}),
			},
		},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(output).NotTo(BeNil())
	g.Expect(output.Volumes).NotTo(BeEmpty())

	rootVolume := output.Volumes[0]
	g.Expect(rootVolume.KmsKeyId).To(HaveValue(Equal(nodePool.Spec.Platform.AWS.RootVolume.EncryptionKey)))
}
