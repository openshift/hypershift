package azure

import (
	"context"
	"fmt"
	"os"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/spf13/cobra"
)

type DestroyInfraOptions struct {
	Name            string
	Location        string
	InfraID         string
	CredentialsFile string
	Credentials     *apifixtures.AzureCreds
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
	creds := o.Credentials
	if creds == nil {
		var err error
		creds, err = readCredentials(o.CredentialsFile)
		if err != nil {
			return fmt.Errorf("failed to read the credentials: %w", err)
		}
	}

	_ = os.Setenv("AZURE_TENANT_ID", creds.TenantID)
	_ = os.Setenv("AZURE_CLIENT_ID", creds.ClientID)
	_ = os.Setenv("AZURE_CLIENT_SECRET", creds.ClientSecret)

	azureCreds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credentials to create image gallery: %w", err)
	}

	resourceGroupClient, err := armresources.NewResourceGroupsClient(creds.SubscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new resource groups client: %w", err)
	}
	destroyFuture, err := resourceGroupClient.BeginDelete(ctx, createResourceGroupName(o.Name, o.InfraID), nil)
	if err != nil {
		return fmt.Errorf("failed to start deletion: %w", err)
	}

	_, err = destroyFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for resourceGroup deletion: %w", err)
	}

	return nil
}
