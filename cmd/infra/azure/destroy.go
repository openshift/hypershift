package azure

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/spf13/cobra"
)

type DestroyInfraOptions struct {
	Name              string
	Location          string
	InfraID           string
	CredentialsFile   string
	Credentials       *util.AzureCreds
	ResourceGroupName string
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys azure infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{
		Location: "eastus",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID(required)")
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to a credentials file (required)")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location where cluster infra should be created")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "The name of the resource group containing the HostedCluster infrastructure resources that need to be destroyed.")

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to destroy infrastructure")
			return err
		}
		log.Log.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd

}

func (o *DestroyInfraOptions) Run(ctx context.Context) error {
	var destroyFuture *runtime.Poller[armresources.ResourceGroupsClientDeleteResponse]

	// Setup subscription ID and Azure credential information
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(log.Log, o.Credentials, o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	// Setup Azure resource group client
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	destroyFuture, err = resourceGroupClient.BeginDelete(ctx, o.GetResourceGroupName(), nil)
	if err != nil {
		return fmt.Errorf("failed to start deletion: %w", err)
	}

	if _, err = destroyFuture.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("failed to wait for resourceGroup deletion: %w", err)
	}

	return nil
}

func (o *DestroyInfraOptions) GetResourceGroupName() string {
	if len(o.ResourceGroupName) > 0 {
		return o.ResourceGroupName
	}
	return o.Name + "-" + o.InfraID
}
