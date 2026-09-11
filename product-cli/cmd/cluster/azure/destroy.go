package azure

import (
	hypershiftazure "github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, util.AzureCredsDestroyDescription)
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, util.LocationDestroyDescription)
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, util.ResourceGroupNameDestroyDescription)
	cmd.Flags().BoolVar(&opts.AzurePlatform.PreserveResourceGroup, "preserve-resource-group", opts.AzurePlatform.PreserveResourceGroup, util.PreserveResourceGroupDescription)
	cmd.Flags().StringVar(&opts.AzurePlatform.DNSZoneRGName, "dns-zone-rg-name", opts.AzurePlatform.DNSZoneRGName, util.DNSZoneRGNameDestroyDescription)

	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("dns-zone-rg-name")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			return err
		}
		return hypershiftazure.DestroyCluster(cmd.Context(), opts, client)
	}

	return cmd
}
