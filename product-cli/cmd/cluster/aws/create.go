package aws

import (
	"context"

	"github.com/spf13/cobra"

	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	awsOpts := hypershiftaws.DefaultOptions()
	hypershiftaws.BindOptions(awsOpts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		err := hypershiftaws.ValidateCreateCredentialInfo(awsOpts.Credentials, awsOpts.CredentialSecretName, opts.Namespace, opts.PullSecretFile)
		if err != nil {
			return err
		}

		if err = hypershiftaws.CreateCluster(ctx, opts, awsOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}
