package aws

import (
	"context"

	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/config"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

func NewCreateCommand(opts *core.RawCreateOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	opts.ReleaseStream = config.DefaultReleaseStream

	awsOpts := hypershiftaws.DefaultOptions()

	hypershiftaws.BindOptions(awsOpts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		createProvider := clientProvider
		var client crclient.Client
		var err error
		if awsOpts.CredentialSecretName != "" {
			client, err = clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
			if err != nil {
				return err
			}
		}
		if err := hypershiftaws.ValidateProductCredentialInfo(cmd.Context(), awsOpts.Credentials, awsOpts.CredentialSecretName, opts.Namespace, client); err != nil {
			return err
		}
		if client != nil {
			provider := *createProvider
			provider.ControllerRuntimeClient = func(_ string) (crclient.Client, error) {
				return client, nil
			}
			createProvider = &provider
		}

		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, awsOpts, createProvider); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}
