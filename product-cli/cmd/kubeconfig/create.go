package kubeconfig

import (
	"context"
	"fmt"
	"io"
	"os"

	hypershiftkubeconfig "github.com/openshift/hypershift/cmd/kubeconfig"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

type options struct {
	namespace   string
	name        string
	portForward bool
}

// renderFunc renders the kubeconfig for the selected HostedCluster(s). It is a
// parameter of newCreateCommand so tests can exercise the flag wiring and the
// error handling without a management cluster.
type renderFunc func(ctx context.Context, namespace string, name string, portForward bool) error

// NewCreateCommand returns a command which can render kubeconfigs for HostedCluster
// resources.
func NewCreateCommand(clientProviders ...*util.ClientProvider) *cobra.Command {
	clientProvider := util.ResolveClientProvider(clientProviders...)
	render := func(ctx context.Context, namespace string, name string, portForward bool) error {
		client, err := clientProvider.ControllerRuntimeClientFor("")
		if err != nil {
			return err
		}
		return hypershiftkubeconfig.Render(ctx, namespace, name, portForward, client)
	}
	return newCreateCommand(render, os.Stderr)
}

func newCreateCommand(render renderFunc, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubeconfig",
		Short:        "Renders kubeconfigs for HostedCluster resources",
		Long:         hypershiftkubeconfig.Description,
		SilenceUsage: true,
	}

	opts := options{
		namespace: "clusters",
	}

	cmd.Flags().StringVar(&opts.namespace, "namespace", opts.namespace, "A HostedCluster namespace. Defaults to 'clusters'.")
	cmd.Flags().StringVar(&opts.name, "name", opts.name, "A HostedCluster name.")
	cmd.Flags().BoolVar(&opts.portForward, "port-forward", false, "For private clusters, rewrite the kubeconfig server URL for use with kubectl port-forward.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := render(cmd.Context(), opts.namespace, opts.name, opts.portForward); err != nil {
			_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
			return err
		}
		return nil
	}

	return cmd
}
