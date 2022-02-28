package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2020-10-01/resources"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/spf13/cobra"
)

type DestroyInfraOptions struct {
	Name            string
	Location        string
	InfraID         string
	CredentialsFile string
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

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("azure-creds")
	cmd.MarkFlagRequired("name")

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
	creds, err := readCredentials(o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read the credentials: %w", err)
	}

	authorizer, err := auth.ClientCredentialsConfig{
		TenantID:     creds.TenantID,
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		AADEndpoint:  azure.PublicCloud.ActiveDirectoryEndpoint,
		Resource:     azure.PublicCloud.ResourceManagerEndpoint,
	}.Authorizer()
	if err != nil {
		return fmt.Errorf("failed to get azure authorizer: %w", err)
	}

	resourceGroupClient := resources.NewGroupsClient(creds.SubscriptionID)
	resourceGroupClient.Authorizer = authorizer
	destroyFuture, err := resourceGroupClient.Delete(ctx, resourceGroupName(o.Name, o.InfraID))
	if err != nil {
		return fmt.Errorf("failed to delete resourceGroup: %w", err)
	}
	if err := destroyFuture.WaitForCompletionRef(ctx, resourceGroupClient.Client); err != nil {
		return fmt.Errorf("failed to wait for resourceGroup deletion: %w", err)
	}

	return nil
}
