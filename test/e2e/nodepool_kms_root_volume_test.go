//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

type KMSRootVolumeTest struct {
	DummyInfraSetup
	hostedCluster *hyperv1.HostedCluster
	clusterOpts   e2eutil.PlatformAgnosticOptions
	ctx           context.Context

	EncryptionKey string
}

func NewKMSRootVolumeTest(ctx context.Context, hostedCluster *hyperv1.HostedCluster, clusterOpts e2eutil.PlatformAgnosticOptions) *KMSRootVolumeTest {
	return &KMSRootVolumeTest{
		hostedCluster: hostedCluster,
		clusterOpts:   clusterOpts,
		ctx:           ctx,
	}
}

func (k *KMSRootVolumeTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}

	t.Log("Starting test KMSRootVolumeTest")

	// find kms key ARN using alias
	kmsKeyArn, err := e2eutil.GetKMSKeyArn(k.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, k.clusterOpts.AWSPlatform.Region, globalOpts.ConfigurableClusterOptions.AWSKmsKeyAlias)
	if err != nil || kmsKeyArn == nil {
		t.Fatalf("failed to retrieve kms key arn")
	}

	k.EncryptionKey = *kmsKeyArn
	if k.EncryptionKey == "" {
		t.Log("empty KMS ARN, using default EBS KMS Key")
	} else {
		t.Logf("retrieved KMS ARN: %s", k.EncryptionKey)
	}
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
		Encrypted:     aws.Bool(true),
		EncryptionKey: k.EncryptionKey,
	}

	return nodePool, nil
}

func (k *KMSRootVolumeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	g.Fail("Induce failure")
	providerID := nodes[0].Spec.ProviderID
	g.Expect(providerID).NotTo(BeEmpty())

	instanceID := providerID[strings.LastIndex(providerID, "/")+1:]
	t.Logf("instanceID: %s", instanceID)

	ec2client := ec2Client(k.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, k.clusterOpts.AWSPlatform.Region)
	output, err := ec2client.DescribeVolumesWithContext(k.ctx, &ec2.DescribeVolumesInput{
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

	if nodePool.Spec.Platform.AWS.RootVolume.EncryptionKey == "" {
		resp, err := ec2client.GetEbsDefaultKmsKeyId(&ec2.GetEbsDefaultKmsKeyIdInput{})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(resp).NotTo(BeNil())
		g.Expect(resp.KmsKeyId).NotTo(BeNil())
		g.Expect(resp.KmsKeyId).To(BeElementOf())
		// When using a default EBS KMS Key, "alias/aws/ebs" will be returned if it hasn't been modified
		// or the actual ID if the default EBS KMS Key is a custom KMS key
		g.Expect(*resp.KmsKeyId).Should(BeElementOf("alias/aws/ebs", *rootVolume.KmsKeyId))
	} else {
		g.Expect(rootVolume.KmsKeyId).To(HaveValue(Equal(nodePool.Spec.Platform.AWS.RootVolume.EncryptionKey)))
	}
}
