package aws

import (
	"context"
	"github.com/spf13/cobra"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformOptions{
		AWSCredentialsFile: "",
		Region:             "us-east-1",
		InstanceType:       "",
		RootVolumeType:     "gp3",
		RootVolumeSize:     120,
		RootVolumeIOPS:     0,
		EndpointAccess:     string(hyperv1.Public),
	}

	cmd.Flags().StringVar(&opts.AWSPlatform.AWSCredentialsFile, "aws-creds", opts.AWSPlatform.AWSCredentialsFile, "Filepath to an AWS credentials file.")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "The region to use for created AWS infrastructure.")
	cmd.Flags().StringSliceVar(&opts.AWSPlatform.Zones, "zones", opts.AWSPlatform.Zones, "The availability zones in which NodePools will be created.")
	cmd.Flags().StringVar(&opts.AWSPlatform.InstanceType, "instance-type", opts.AWSPlatform.InstanceType, "The instance type for the NodePool machines.")
	cmd.Flags().StringVar(&opts.AWSPlatform.RootVolumeType, "root-volume-type", opts.AWSPlatform.RootVolumeType, "The type of the root volume for the NodePool machines (ex. gp3, io2).")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeIOPS, "root-volume-iops", opts.AWSPlatform.RootVolumeIOPS, "The IOPS of the root volume when specifying type:io1 for the NodePool machines.")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeSize, "root-volume-size", opts.AWSPlatform.RootVolumeSize, "The size of the root volume for machines in the NodePool.")
	cmd.Flags().StringVar(&opts.AWSPlatform.RootVolumeEncryptionKey, "root-volume-kms-key", opts.AWSPlatform.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for the NodePool machines.")
	cmd.Flags().StringSliceVar(&opts.AWSPlatform.AdditionalTags, "additional-tags", opts.AWSPlatform.AdditionalTags, "Additional tags to set on created AWS resources.")
	cmd.Flags().StringVar(&opts.AWSPlatform.EndpointAccess, "endpoint-access", opts.AWSPlatform.EndpointAccess, "The endpoint access for the control plane endpoints (ex. Public, PublicAndPrivate, Private).")
	cmd.Flags().StringVar(&opts.AWSPlatform.EtcdKMSKeyARN, "kms-key-arn", opts.AWSPlatform.EtcdKMSKeyARN, "The ARN of the KMS key to use for etcd encryption; if this is not supplied, etcd encryption will default to using a generated AESCBC key.")
	cmd.Flags().BoolVar(&opts.AWSPlatform.EnableProxy, "enable-proxy", opts.AWSPlatform.EnableProxy, "Enables if a proxy should be set up for internet connectivity, rather than allowing direct internet access from the the NodePool machines.")
	cmd.Flags().StringVar(&opts.CredentialSecretName, "secret-creds", opts.CredentialSecretName, "A Kubernetes secret with needed AWS platform credentials: --aws-creds, --pull-secret, and --base-domain value. The secret must exist in the supplied \"--namespace\".")
	cmd.Flags().StringVar(&opts.AWSPlatform.IssuerURL, "oidc-issuer-url", "", "The OIDC provider issuer URL.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		err := hypershiftaws.ValidateCreateCredentialInfo(opts)
		if err != nil {
			return err
		}

		if err = hypershiftaws.CreateCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}
