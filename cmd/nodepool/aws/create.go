package aws

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

type AWSPlatformCreateOptions struct {
	InstanceProfile         string
	SubnetID                string
	SecurityGroupID         string
	InstanceType            string
	RootVolumeType          string
	RootVolumeIOPS          int64
	RootVolumeSize          int64
	RootVolumeEncryptionKey string
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &AWSPlatformCreateOptions{
		InstanceType:   "",
		RootVolumeType: "gp3",
		RootVolumeSize: 120,
		RootVolumeIOPS: 0,
	}
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional NodePool resources for AWS platform",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "The AWS instance type of the NodePool")
	cmd.Flags().StringVar(&platformOpts.SubnetID, "subnet-id", platformOpts.SubnetID, "The AWS subnet ID in which to create the NodePool")
	cmd.Flags().StringVar(&platformOpts.SecurityGroupID, "securitygroup-id", platformOpts.SecurityGroupID, "The AWS security group in which to create the NodePool")
	cmd.Flags().StringVar(&platformOpts.InstanceProfile, "instance-profile", platformOpts.InstanceProfile, "The AWS instance profile for the NodePool")
	cmd.Flags().StringVar(&platformOpts.RootVolumeType, "root-volume-type", platformOpts.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeIOPS, "root-volume-iops", platformOpts.RootVolumeIOPS, "The iops of the root volume for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeSize, "root-volume-size", platformOpts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	cmd.Flags().StringVar(&platformOpts.RootVolumeEncryptionKey, "root-volume-kms-key", platformOpts.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for machines in the NodePool")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *AWSPlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, _ crclient.Client) error {
	if len(o.InstanceProfile) == 0 {
		o.InstanceProfile = fmt.Sprintf("%s-worker", hcluster.Spec.InfraID)
	}
	if len(o.SubnetID) == 0 {
		if hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
			o.SubnetID = *hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID
		} else {
			return fmt.Errorf("subnet ID was not specified and cannot be determined from HostedCluster")
		}
	}

	var instanceType string
	if o.InstanceType != "" {
		instanceType = o.InstanceType
	} else {
		// Aligning with AWS IPI instance type defaults
		switch nodePool.Spec.Arch {
		case "amd64":
			instanceType = "m5.large"
		case "arm64":
			instanceType = "m6g.large"
		}
	}

	nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
		InstanceType:    instanceType,
		InstanceProfile: o.InstanceProfile,
		Subnet: hyperv1.AWSResourceReference{
			ID: &o.SubnetID,
		},
		RootVolume: &hyperv1.Volume{
			Type:          o.RootVolumeType,
			Size:          o.RootVolumeSize,
			IOPS:          o.RootVolumeIOPS,
			EncryptionKey: o.RootVolumeEncryptionKey,
		},
	}
	if len(o.SecurityGroupID) > 0 {
		nodePool.Spec.Platform.AWS.SecurityGroups = []hyperv1.AWSResourceReference{
			{ID: &o.SecurityGroupID},
		}
	}
	return nil
}

func (o *AWSPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AWSPlatform
}
