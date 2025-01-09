package aws

import (
	hypershiftaws "github.com/openshift/hypershift/cmd/nodepool/aws"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"github.com/spf13/cobra"
)

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &hypershiftaws.AWSPlatformCreateOptions{
		InstanceType:   "",
		RootVolumeIOPS: 0,
		RootVolumeSize: 120,
		RootVolumeType: "gp3",
	}
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional NodePool resources for AWS platform",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "The AWS instance type of the NodePool.")
	cmd.Flags().StringVar(&platformOpts.SubnetID, "subnet-id", platformOpts.SubnetID, "The AWS subnet ID in which to create the NodePool.")
	cmd.Flags().StringVar(&platformOpts.SecurityGroupID, "securitygroup-id", platformOpts.SecurityGroupID, "The AWS security group in which to create the NodePool.")
	cmd.Flags().StringVar(&platformOpts.InstanceProfile, "instance-profile", platformOpts.InstanceProfile, "The AWS instance profile for the NodePool.")
	cmd.Flags().StringVar(&platformOpts.RootVolumeType, "root-volume-type", platformOpts.RootVolumeType, "The type of the root volume for the NodePool machines.")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeIOPS, "root-volume-iops", platformOpts.RootVolumeIOPS, "The IOPS of the root volume for the NodePool machines.")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeSize, "root-volume-size", platformOpts.RootVolumeSize, "The size of the root volume for the NodePool machines.")
	cmd.Flags().StringVar(&platformOpts.RootVolumeEncryptionKey, "root-volume-kms-key", platformOpts.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for the NodePool machines.")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}
