package aws

import (
	"github.com/spf13/cobra"

	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys a HostedCluster and its associated infrastructure on AWS",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformDestroyOptions{
		AWSCredentialsFile: "",
		PreserveIAM:        false,
		Region:             "us-east-1",
	}

	cmd.Flags().StringVar(&opts.AWSPlatform.AWSCredentialsFile, "aws-creds", opts.AWSPlatform.AWSCredentialsFile, "Filepath to an AWS credentials file.")
	cmd.Flags().BoolVar(&opts.AWSPlatform.PreserveIAM, "preserve-iam", opts.AWSPlatform.PreserveIAM, "If set to true, skip deleting IAM. Otherwise, destroy any default generated IAM along with other infrastructure.")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "A HostedCluster's region.")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomain, "base-domain", opts.AWSPlatform.BaseDomain, "A HostedCluster's base domain.")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomainPrefix, "base-domain-prefix", opts.AWSPlatform.BaseDomainPrefix, "A HostedCluster's base domain prefix.")
	cmd.Flags().StringVar(&opts.CredentialSecretName, "secret-creds", opts.CredentialSecretName, "A Kubernetes secret with a platform credentials: pull-secret and base-domain. The secret must exist in the supplied \"--namespace\".")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := hypershiftaws.ValidateCredentialInfo(opts)
		if err != nil {
			return err
		}

		if err = hypershiftaws.DestroyCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}
