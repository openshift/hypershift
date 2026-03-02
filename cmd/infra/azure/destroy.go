package azure

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type DestroyInfraOptions struct {
	Name                  string
	Location              string
	InfraID               string
	CredentialsFile       string
	Credentials           *util.AzureCreds
	ResourceGroupName     string
	PreserveResourceGroup bool
	Cloud                 string

	// clientOptions allows injecting custom Azure client options for testing.
	// When nil, default options are created based on the Cloud field.
	clientOptions *arm.ClientOptions
	// azureCredential allows injecting custom Azure credentials for testing.
	// When nil, credentials are created from Credentials or CredentialsFile.
	azureCredential azcore.TokenCredential
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys azure infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{
		Location: config.DefaultAzureLocation,
		Cloud:    config.DefaultAzureCloud,
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID(required)")
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to a credentials file (required)")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location where cluster infra should be created")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "Azure cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud)")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "The name of the resource group containing the HostedCluster infrastructure resources that need to be destroyed.")
	cmd.Flags().BoolVar(&opts.PreserveResourceGroup, "preserve-resource-group", opts.PreserveResourceGroup, "When true, the managed/main resource group will not be deleted during cluster destroy. Only cluster-specific resources within the resource group will be cleaned up.")

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

// DefaultDestroyOptions returns DestroyInfraOptions with default values for self-managed Azure
func DefaultDestroyOptions() *DestroyInfraOptions {
	return &DestroyInfraOptions{
		Location: config.DefaultAzureLocation,
		Cloud:    config.DefaultAzureCloud,
	}
}

// BindDestroyProductFlags binds flags for the product CLI (hcp) infra destroy azure command.
// This exposes only the self-managed Azure flags relevant for the productized CLI.
func BindDestroyProductFlags(opts *DestroyInfraOptions, flags *pflag.FlagSet) {
	// Required flags
	flags.StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)
	flags.StringVar(&opts.Name, "name", opts.Name, "A name for the HostedCluster")

	// Location and cloud
	flags.StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	flags.StringVar(&opts.Cloud, "cloud", opts.Cloud, "Azure cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud)")

	// Resource group
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	flags.BoolVar(&opts.PreserveResourceGroup, "preserve-resource-group", opts.PreserveResourceGroup, util.PreserveResourceGroupDescription)
}

// Validate validates the DestroyInfraOptions before running the destroy operation.
func (o *DestroyInfraOptions) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("name is required")
	}
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.CredentialsFile == "" && o.Credentials == nil {
		return fmt.Errorf("azure-creds is required")
	}
	return nil
}

func (o *DestroyInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	additionalResourceGroups := []string{
		o.Name + "-vnet-" + o.InfraID,
		o.Name + "-nsg-" + o.InfraID,
	}

	// Use injected credential if provided (for testing), otherwise create from config
	var subscriptionID string
	var azureCreds azcore.TokenCredential
	if o.azureCredential != nil {
		azureCreds = o.azureCredential
		if o.Credentials != nil {
			subscriptionID = o.Credentials.SubscriptionID
		}
	} else {
		var err error
		subscriptionID, azureCreds, err = util.SetupAzureCredentials(logger, o.Credentials, o.CredentialsFile)
		if err != nil {
			return fmt.Errorf("failed to setup Azure credentials: %w", err)
		}
	}

	// Use injected client options if provided (for testing), otherwise create default options
	clientOptions := o.clientOptions
	if clientOptions == nil {
		// Setup cloud configuration
		cloudConfig, err := azureutil.GetAzureCloudConfiguration(o.Cloud)
		if err != nil {
			return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
		}
		clientOptions = &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}
	}

	// Setup Azure resource group client
	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	// Setup Azure resources client for per-resource deletion
	resourcesClient, err := armresources.NewClient(subscriptionID, azureCreds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create new resources client: %w", err)
	}

	mainResourceGroup := o.GetResourceGroupName()

	// Handle main resource group based on preserve flag
	if o.PreserveResourceGroup {
		logger.Info("Preserving main resource group, deleting only cluster-specific resources", "resource-group", mainResourceGroup)
		if err := o.deleteClusterResourcesInGroup(ctx, logger, resourcesClient, mainResourceGroup); err != nil {
			return fmt.Errorf("failed to delete cluster resources in resource group %s: %w", mainResourceGroup, err)
		}
		logger.Info("Successfully cleaned up cluster resources, resource group preserved", "resource-group", mainResourceGroup)
	} else {
		logger.Info("Deleting main resource group", "resource-group", mainResourceGroup)
		if err := o.deleteResourceGroup(ctx, logger, resourceGroupClient, mainResourceGroup); err != nil {
			return err
		}
	}

	// Always delete additional resource groups (vnet, nsg)
	for _, rg := range additionalResourceGroups {
		exists, err := resourceGroupClient.CheckExistence(ctx, rg, nil)
		if err != nil {
			return fmt.Errorf("failed to check existence of resource group %s: %w", rg, err)
		}
		if exists.Success {
			logger.Info("Deleting additional resource group", "resource-group", rg)
			if err := o.deleteResourceGroup(ctx, logger, resourceGroupClient, rg); err != nil {
				return err
			}
		}
	}

	return nil
}

// deleteResourceGroup deletes an Azure resource group and waits for the deletion to complete.
// It handles the 404 "not found" case gracefully by logging and returning nil.
func (o *DestroyInfraOptions) deleteResourceGroup(ctx context.Context, logger logr.Logger, client *armresources.ResourceGroupsClient, resourceGroup string) error {
	poller, err := client.BeginDelete(ctx, resourceGroup, nil)
	if err != nil {
		if azureutil.IsNotFoundError(err) {
			logger.Info("Resource group not found, skipping deletion", "resource-group", resourceGroup)
			return nil
		}
		return fmt.Errorf("failed to start deletion for resource group %s: %w", resourceGroup, err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("failed to wait for resource group deletion %s: %w", resourceGroup, err)
	}
	return nil
}

// resourceToDelete represents a resource to be deleted with its ID, API version, and type.
type resourceToDelete struct {
	id           string
	apiVersion   string
	name         string
	resourceType string
}

// deleteClusterResourcesInGroup deletes cluster-specific resources within a resource group
// while preserving the resource group itself and any non-cluster resources.
// Resources are identified as cluster-specific if they contain the infraID in their name OR
// if they match the cluster naming pattern (e.g., {name}-azurecluster.{baseDomain} for DNS zones).
// Resources are deleted in dependency order to avoid conflicts.
func (o *DestroyInfraOptions) deleteClusterResourcesInGroup(ctx context.Context, logger logr.Logger, resourcesClient *armresources.Client, resourceGroupName string) error {
	// List all resources in the resource group
	pager := resourcesClient.NewListByResourceGroupPager(resourceGroupName, nil)

	var resourcesToDelete []resourceToDelete
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources in resource group %s: %w", resourceGroupName, err)
		}

		for _, resource := range page.Value {
			if resource.ID == nil || resource.Name == nil || resource.Type == nil {
				continue
			}

			// Only delete resources that are cluster-specific
			// Resources are identified as cluster-specific if they contain the InfraID OR
			// if they match the cluster naming pattern (e.g., {name}-azurecluster.{baseDomain} for DNS zones)
			isClusterResource := strings.Contains(*resource.Name, o.InfraID) ||
				strings.HasPrefix(*resource.Name, o.Name+"-azurecluster.")

			if isClusterResource {
				apiVersion := getAPIVersionForResourceType(*resource.Type)
				resourcesToDelete = append(resourcesToDelete, resourceToDelete{
					id:           *resource.ID,
					apiVersion:   apiVersion,
					name:         *resource.Name,
					resourceType: *resource.Type,
				})
				logger.Info("Marking cluster resource for deletion", "resource", *resource.Name, "id", *resource.ID, "type", *resource.Type)
			} else {
				logger.Info("Preserving non-cluster resource", "resource", *resource.Name)
			}
		}
	}

	// Sort resources by deletion priority to handle basic dependencies
	sortResourcesByDeletionOrder(resourcesToDelete)

	// Delete the identified cluster resources in order
	var deletionErrors []error
	successfulDeletions := 0

	for _, resource := range resourcesToDelete {
		logger.Info("Deleting cluster resource", "resource-id", resource.id, "resource-type", resource.resourceType)
		poller, err := resourcesClient.BeginDeleteByID(ctx, resource.id, resource.apiVersion, nil)
		if err != nil {
			if azureutil.IsNotFoundError(err) {
				logger.Info("Resource not found, skipping", "resource-id", resource.id)
				continue
			}
			logger.Error(err, "Failed to start deletion for resource, continuing with remaining resources", "resource-id", resource.id, "resource-name", resource.name)
			deletionErrors = append(deletionErrors, fmt.Errorf("failed to start deletion for resource %s (%s): %w", resource.name, resource.id, err))
			continue
		}

		if _, err = poller.PollUntilDone(ctx, nil); err != nil {
			logger.Error(err, "Failed to complete deletion for resource, continuing with remaining resources", "resource-id", resource.id, "resource-name", resource.name)
			deletionErrors = append(deletionErrors, fmt.Errorf("failed to complete deletion for resource %s (%s): %w", resource.name, resource.id, err))
			continue
		}
		logger.Info("Successfully deleted cluster resource", "resource-id", resource.id)
		successfulDeletions++
	}

	logger.Info("Cluster resource cleanup summary", "resources-deleted", successfulDeletions, "total-resources", len(resourcesToDelete), "errors", len(deletionErrors))

	// If there were any deletion errors, log them
	if len(deletionErrors) > 0 {
		logger.Info("Some resources failed to delete, but continuing with destroy operation", "failed-count", len(deletionErrors))
		for i, err := range deletionErrors {
			logger.Error(err, "Deletion error", "error-number", i+1)
		}
		// Return nil to allow the destroy operation to continue
		// The errors have been logged for user visibility
	}

	return nil
}

// sortResourcesByDeletionOrder sorts resources so that dependencies are deleted before their dependents.
// The deletion order is:
// 1. Virtual network links (child resources)
// 2. Virtual machines
// 3. Network interfaces
// 4. Load balancers
// 5. Public IP addresses
// 6. Disks
// 7. Network security groups
// 8. Virtual networks
// 9. Private DNS zones (parent resources)
// 10. Storage accounts
// 11. Managed identities
// 12. Everything else
func sortResourcesByDeletionOrder(resources []resourceToDelete) {
	priority := func(resourceType string) int {
		switch {
		case strings.Contains(resourceType, "virtualNetworkLinks"):
			return 1
		case strings.Contains(resourceType, "virtualMachines"):
			return 2
		case strings.Contains(resourceType, "networkInterfaces"):
			return 3
		case strings.Contains(resourceType, "loadBalancers"):
			return 4
		case strings.Contains(resourceType, "publicIPAddresses"):
			return 5
		case strings.Contains(resourceType, "disks"):
			return 6
		case strings.Contains(resourceType, "networkSecurityGroups"):
			return 7
		case strings.Contains(resourceType, "virtualNetworks"):
			return 8
		case strings.Contains(resourceType, "privateDnsZones") && !strings.Contains(resourceType, "virtualNetworkLinks"):
			return 9
		case strings.Contains(resourceType, "storageAccounts"):
			return 10
		case strings.Contains(resourceType, "userAssignedIdentities"):
			return 11
		default:
			return 99
		}
	}

	// Sort by priority (lower number = delete first)
	sort.Slice(resources, func(i, j int) bool {
		return priority(resources[i].resourceType) < priority(resources[j].resourceType)
	})
}

// getAPIVersionForResourceType returns the appropriate API version for a given Azure resource type.
// This function maps common Azure resource types to their stable API versions.
func getAPIVersionForResourceType(resourceType string) string {
	// Map of resource types to their API versions
	apiVersions := map[string]string{
		"Microsoft.Network/publicIPAddresses":                   "2023-11-01",
		"Microsoft.Network/loadBalancers":                       "2023-11-01",
		"Microsoft.Network/networkInterfaces":                   "2023-11-01",
		"Microsoft.Network/networkSecurityGroups":               "2023-11-01",
		"Microsoft.Network/virtualNetworks":                     "2023-11-01",
		"Microsoft.Network/privateDnsZones":                     "2020-06-01",
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks": "2020-06-01",
		"Microsoft.Compute/virtualMachines":                     "2024-03-01",
		"Microsoft.Compute/disks":                               "2023-10-02",
		"Microsoft.Storage/storageAccounts":                     "2023-01-01",
		"Microsoft.ManagedIdentity/userAssignedIdentities":      "2023-01-31",
	}

	// Check if we have a specific API version for this resource type
	if apiVersion, ok := apiVersions[resourceType]; ok {
		return apiVersion
	}

	// Default to a common API version that works for most resource types
	return "2021-04-01"
}

// GetResourceGroupName returns the resource group name to use for destroy operations.
// If a custom resource group name was provided, it is returned; otherwise, the default
// name format of {name}-{infraID} is used.
func (o *DestroyInfraOptions) GetResourceGroupName() string {
	if len(o.ResourceGroupName) > 0 {
		return o.ResourceGroupName
	}
	return o.Name + "-" + o.InfraID
}
