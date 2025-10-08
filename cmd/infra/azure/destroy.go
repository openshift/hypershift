package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/go-logr/logr"
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

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to destroy infrastructure")
			return err
		}
		logger.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd

}

func (o *DestroyInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	var additionalResourceGroups = []string{
		o.Name + "-vnet-" + o.InfraID,
		o.Name + "-nsg-" + o.InfraID,
	}
	var destroyFuture *runtime.Poller[armresources.ResourceGroupsClientDeleteResponse]

	// Setup subscription ID and Azure credential information
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(logger, o.Credentials, o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	// Setup Azure resource group client
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	managedRGName := o.GetResourceGroupName()
	userProvidedRG := len(o.ResourceGroupName) > 0

	// If a managed resource group was provided by the user, delete cluster-specific resources within it
	// instead of deleting the entire resource group
	if userProvidedRG {
		logger.Info("Preserving managed resource group, deleting cluster-specific resources only", "resource-group", managedRGName)
		if err := o.deleteClusterResourcesFromManagedRG(ctx, subscriptionID, azureCreds, managedRGName, logger); err != nil {
			return fmt.Errorf("failed to delete cluster resources from managed resource group: %w", err)
		}
		logger.Info("Successfully preserved managed resource group", "resource-group", managedRGName)
	} else {
		// If no managed RG was provided, delete the auto-generated resource group entirely (legacy behavior)
		logger.Info("Deleting auto-generated resource group", "resource-group", managedRGName)
		destroyFuture, err = resourceGroupClient.BeginDelete(ctx, managedRGName, nil)
		if err != nil {
			if strings.Contains(err.Error(), "ResourceGroupNotFound") {
				logger.Info("Resource group not found, continuing with infra deletion", "resource-group", managedRGName)
			} else {
				return fmt.Errorf("failed to start deletion for resource group %s: %w", managedRGName, err)
			}
		} else {
			if _, err = destroyFuture.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("failed to wait for resource group deletion %s: %w", managedRGName, err)
			}
		}
	}

	// Delete additional cluster-specific resource groups (vnet, nsg)
	for _, rg := range additionalResourceGroups {
		exists, err := resourceGroupClient.CheckExistence(ctx, rg, nil)
		if err != nil {
			return fmt.Errorf("failed to check existence of resource group %s: %w", rg, err)
		}
		if exists.Success {
			logger.Info("Deleting cluster-specific resource group", "resource-group", rg)
			destroyFuture, err = resourceGroupClient.BeginDelete(ctx, rg, nil)
			if err != nil {
				if strings.Contains(err.Error(), "ResourceGroupNotFound") {
					logger.Info("Resource group not found, continuing with infra deletion", "resource-group", rg)
					continue
				}
				return fmt.Errorf("failed to start deletion for resource group %s: %w", rg, err)
			}

			if _, err = destroyFuture.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("failed to wait for resource group deletion %s: %w", rg, err)
			}
		}
	}

	return nil
}

// deleteClusterResourcesFromManagedRG deletes cluster-specific resources from a managed resource group
// by filtering resources based on infraID tags or name patterns
func (o *DestroyInfraOptions) deleteClusterResourcesFromManagedRG(ctx context.Context, subscriptionID string, azureCreds *azidentity.DefaultAzureCredential, resourceGroupName string, logger logr.Logger) error {
	// Setup Azure resources client
	resourcesClient, err := armresources.NewClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new resources client: %w", err)
	}

	// List all resources in the resource group and filter by infraID tag or name pattern
	pager := resourcesClient.NewListByResourceGroupPager(resourceGroupName, nil)

	var resourcesToDelete []string
	clusterNamePrefix := o.Name + "-" + o.InfraID

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources in resource group %s: %w", resourceGroupName, err)
		}

		for _, resource := range page.Value {
			if resource.ID == nil {
				continue
			}

			// Check if resource belongs to this cluster by:
			// 1. Checking for infraID tag
			// 2. Checking if resource name contains cluster name-infraID prefix
			shouldDelete := false

			// Check tags for infraID
			if resource.Tags != nil {
				if infraIDTag, exists := resource.Tags["infraID"]; exists && infraIDTag != nil && *infraIDTag == o.InfraID {
					shouldDelete = true
				}
				// Also check for kubernetes.io/cluster tag which is used by CAPI
				clusterTag := fmt.Sprintf("kubernetes.io/cluster/%s", clusterNamePrefix)
				if _, exists := resource.Tags[clusterTag]; exists {
					shouldDelete = true
				}
			}

			// Check resource name pattern as fallback
			if !shouldDelete && resource.Name != nil {
				resourceName := *resource.Name
				// Check if resource name contains the cluster prefix
				if strings.Contains(resourceName, clusterNamePrefix) || strings.Contains(resourceName, o.InfraID) {
					shouldDelete = true
				}
			}

			if shouldDelete {
				resourcesToDelete = append(resourcesToDelete, *resource.ID)
			}
		}
	}

	if len(resourcesToDelete) == 0 {
		logger.Info("No cluster-specific resources found to delete", "resource-group", resourceGroupName)
		return nil
	}

	logger.Info("Found cluster-specific resources to delete", "resource-group", resourceGroupName, "count", len(resourcesToDelete))

	// Delete each cluster-specific resource
	for _, resourceID := range resourcesToDelete {
		logger.Info("Deleting cluster resource", "resource-id", resourceID)

		// Use a generic API version for deletion
		// This works for most Azure resources
		poller, err := resourcesClient.BeginDeleteByID(ctx, resourceID, "2021-04-01", nil)
		if err != nil {
			logger.Error(err, "Failed to start deletion for resource", "resource-id", resourceID)
			continue
		}

		if _, err = poller.PollUntilDone(ctx, nil); err != nil {
			logger.Error(err, "Failed to wait for resource deletion", "resource-id", resourceID)
			continue
		}
		logger.Info("Successfully deleted cluster resource", "resource-id", resourceID)
	}

	logger.Info("Completed deletion of cluster-specific resources", "resource-group", resourceGroupName, "deleted-count", len(resourcesToDelete))
	return nil
}

func (o *DestroyInfraOptions) GetResourceGroupName() string {
	if len(o.ResourceGroupName) > 0 {
		return o.ResourceGroupName
	}
	return o.Name + "-" + o.InfraID
}
