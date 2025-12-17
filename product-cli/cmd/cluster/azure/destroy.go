package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, "The name of the resource group containing the HostedCluster infrastructure resources that need to be destroyed.")
	cmd.Flags().BoolVar(&opts.AzurePlatform.PreserveResourceGroup, "preserve-resource-group", opts.AzurePlatform.PreserveResourceGroup, "When true, the managed/main resource group will not be deleted during cluster destroy. Only cluster-specific resources within the resource group will be cleaned up.")

	_ = cmd.MarkFlagRequired("azure-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := hypershiftazure.DestroyCluster(cmd.Context(), opts); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}

	return cmd
}
