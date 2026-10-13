package aws

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

type RawAWSPlatformCreateOptions struct {
	*AWSPlatformCreateOptions
}

func DefaultOptions() *RawAWSPlatformCreateOptions {
	return &RawAWSPlatformCreateOptions{
		AWSPlatformCreateOptions: &AWSPlatformCreateOptions{
			InstanceType:   "",
			RootVolumeType: "gp3",
			RootVolumeSize: 120,
			RootVolumeIOPS: 0,
		},
	}
}

// validatedAWSPlatformCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedAWSPlatformCreateOptions struct {
	*RawAWSPlatformCreateOptions
}

type ValidatedAWSPlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedAWSPlatformCreateOptions
}

// completedAWSPlatformCreateOptions is a private wrapper that enforces a call of Complete() before nodepool creation can be invoked.
type completedAWSPlatformCreateOptions struct {
	*AWSPlatformCreateOptions
}

type CompletedAWSPlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedAWSPlatformCreateOptions
}

// Validate validates the AWS nodepool platform options.
// This method uses the unified signature pattern defined in core.NodePoolPlatformValidator.
func (o *RawAWSPlatformCreateOptions) Validate(_ context.Context, _ *core.CreateNodePoolOptions) (core.NodePoolPlatformCompleter, error) {
	// Validate root volume size minimum
	if o.RootVolumeSize < 8 {
		return nil, fmt.Errorf("root volume size must be at least 8 GB, got %d", o.RootVolumeSize)
	}

	return &ValidatedAWSPlatformCreateOptions{
		validatedAWSPlatformCreateOptions: &validatedAWSPlatformCreateOptions{
			RawAWSPlatformCreateOptions: o,
		},
	}, nil
}

// Complete completes the AWS nodepool platform options.
// This method uses the unified signature pattern defined in core.NodePoolPlatformCompleter.
func (o *ValidatedAWSPlatformCreateOptions) Complete(_ context.Context, _ *core.CreateNodePoolOptions) (core.PlatformOptions, error) {
	return &CompletedAWSPlatformCreateOptions{
		completedAWSPlatformCreateOptions: &completedAWSPlatformCreateOptions{
			AWSPlatformCreateOptions: o.AWSPlatformCreateOptions,
		},
	}, nil
}

func BindDeveloperOptions(opts *RawAWSPlatformCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "The AWS instance type of the NodePool")
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The AWS subnet ID in which to create the NodePool")
	flags.StringVar(&opts.SecurityGroupID, "securitygroup-id", opts.SecurityGroupID, "The AWS security group in which to create the NodePool")
	flags.StringVar(&opts.InstanceProfile, "instance-profile", opts.InstanceProfile, "The AWS instance profile for the NodePool")
	flags.StringVar(&opts.RootVolumeType, "root-volume-type", opts.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	flags.Int64Var(&opts.RootVolumeIOPS, "root-volume-iops", opts.RootVolumeIOPS, "The iops of the root volume for machines in the NodePool")
	flags.Int64Var(&opts.RootVolumeSize, "root-volume-size", opts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	flags.StringVar(&opts.RootVolumeEncryptionKey, "root-volume-kms-key", opts.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for machines in the NodePool")
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := DefaultOptions()
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional NodePool resources for AWS platform",
		SilenceUsage: true,
	}

	BindDeveloperOptions(platformOpts, cmd.Flags())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		validOpts, err := platformOpts.Validate(ctx, coreOpts)
		if err != nil {
			return err
		}

		opts, err := validOpts.Complete(ctx, coreOpts)
		if err != nil {
			return err
		}
		return coreOpts.CreateRunFunc(opts)(cmd, args)
	}

	return cmd
}

func (o *CompletedAWSPlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, _ crclient.Client) error {
	instanceProfile := o.InstanceProfile
	if len(instanceProfile) == 0 {
		instanceProfile = fmt.Sprintf("%s-worker", hcluster.Spec.InfraID)
	}

	subnetID := o.SubnetID
	if len(subnetID) == 0 {
		if hcluster.Spec.Platform.AWS != nil &&
			hcluster.Spec.Platform.AWS.CloudProviderConfig != nil &&
			hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet != nil &&
			hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
			subnetID = *hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID
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
		InstanceProfile: instanceProfile,
		Subnet: hyperv1.AWSResourceReference{
			ID: &subnetID,
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

func (o *CompletedAWSPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AWSPlatform
}
