package azure

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys a hHostedCluster and its resources on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")

	cmd.MarkFlagRequired("azure-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := DestroyCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}
func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {

	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}
	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		o.AzurePlatform.Location = hostedCluster.Spec.Platform.Azure.Location
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if o.AzurePlatform.Location == "" {
		inputErrors = append(inputErrors, fmt.Errorf("location is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	return (&azureinfra.DestroyInfraOptions{
		Name:            o.Name,
		Location:        o.AzurePlatform.Location,
		InfraID:         o.InfraID,
		CredentialsFile: o.AzurePlatform.CredentialsFile,
	}).Run(ctx)
}
