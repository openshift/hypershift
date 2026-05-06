package azure

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nonRetriableError matches the Azure SDK's errorinfo.NonRetriable interface
// for use with errors.As without importing the internal package.
type nonRetriableError interface {
	error
	NonRetriable()
}

const (
	DefaultInfraGracePeriod         = 5 * time.Minute
	defaultRetryInterval            = 10 * time.Second
	fallbackAzureResourceAPIVersion = "2021-04-01"
)

// resourceDeleter abstracts Azure resource listing and deletion for testability.
type resourceDeleter interface {
	ListByResourceGroup(ctx context.Context, resourceGroup string) ([]resourceToDelete, error)
	DeleteByID(ctx context.Context, id string, apiVersion string) error
}

// azureResourceDeleter wraps *armresources.Client to implement resourceDeleter.
type azureResourceDeleter struct {
	client *armresources.Client
}

func (a *azureResourceDeleter) ListByResourceGroup(ctx context.Context, resourceGroup string) ([]resourceToDelete, error) {
	pager := a.client.NewListByResourceGroupPager(resourceGroup, nil)
	var resources []resourceToDelete
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resources in resource group %s: %w", resourceGroup, err)
		}
		for _, resource := range page.Value {
			if resource.ID == nil || resource.Name == nil || resource.Type == nil {
				continue
			}
			resources = append(resources, resourceToDelete{
				id:           *resource.ID,
				apiVersion:   getAPIVersionForResourceType(*resource.Type),
				name:         *resource.Name,
				resourceType: *resource.Type,
			})
		}
	}
	return resources, nil
}

func (a *azureResourceDeleter) DeleteByID(ctx context.Context, id string, apiVersion string) error {
	poller, err := a.client.BeginDeleteByID(ctx, id, apiVersion, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

type DestroyInfraOptions struct {
	Name                  string
	Location              string
	InfraID               string
	CredentialsFile       string
	Credentials           *util.AzureCreds
	ResourceGroupName     string
	PreserveResourceGroup bool
	Cloud                 string
	AzureInfraGracePeriod time.Duration

	retryInterval time.Duration // test-only: overrides defaultRetryInterval for faster test execution
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
	cmd.Flags().DurationVar(&opts.AzureInfraGracePeriod, "azure-infra-grace-period", DefaultInfraGracePeriod, util.AzureInfraGracePeriodDescription)

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
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
	flags.DurationVar(&opts.AzureInfraGracePeriod, "azure-infra-grace-period", DefaultInfraGracePeriod, util.AzureInfraGracePeriodDescription)
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
	if o.AzureInfraGracePeriod < 0 {
		return fmt.Errorf("azure-infra-grace-period must be >= 0")
	}
	return nil
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

	// Setup cloud configuration
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(o.Cloud)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

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

	mainResourceGroup := o.getResourceGroupName()

	deleter := &azureResourceDeleter{client: resourcesClient}

	// Handle main resource group based on preserve flag
	if o.PreserveResourceGroup {
		logger.Info("Preserving main resource group, deleting only cluster-specific resources", "resource-group", mainResourceGroup)
		if err := o.retryDeleteClusterResources(ctx, logger, deleter, mainResourceGroup); err != nil {
			return fmt.Errorf("failed to delete cluster resources in resource group %s: %w", mainResourceGroup, err)
		}
		logger.Info("Successfully cleaned up cluster resources, resource group preserved", "resource-group", mainResourceGroup)
	} else {
		logger.Info("Deleting main resource group", "resource-group", mainResourceGroup)
		destroyFuture, err = resourceGroupClient.BeginDelete(ctx, mainResourceGroup, nil)
		if err != nil {
			if strings.Contains(err.Error(), "ResourceGroupNotFound") {
				logger.Info("Resource group not found, continuing with infra deletion", "resource-group", mainResourceGroup)
			} else {
				return fmt.Errorf("failed to start deletion for resource group %s: %w", mainResourceGroup, err)
			}
		} else {
			if _, err = destroyFuture.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("failed to wait for resource group deletion %s: %w", mainResourceGroup, err)
			}
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

func (o *DestroyInfraOptions) isClusterResource(name string) bool {
	return strings.Contains(name, o.InfraID) ||
		strings.HasPrefix(name, o.Name+"-azurecluster.")
}

// resourceToDelete is an internal transfer object for resource deletion operations.
type resourceToDelete struct {
	id           string
	apiVersion   string
	name         string
	resourceType string
}

func (o *DestroyInfraOptions) retryDeleteClusterResources(ctx context.Context, logger logr.Logger, deleter resourceDeleter, resourceGroupName string) error {
	gracePeriod := o.AzureInfraGracePeriod
	retryInterval := o.retryInterval
	if retryInterval == 0 {
		retryInterval = defaultRetryInterval
	}

	infraCtx, cancel := context.WithTimeout(ctx, gracePeriod)
	defer cancel()

	logger.Info("Starting cluster resource cleanup with retry", "resource-group", resourceGroupName, "grace-period", gracePeriod)

	pollErr := wait.PollUntilContextCancel(infraCtx, retryInterval, true, func(pollCtx context.Context) (bool, error) {
		err := o.deleteClusterResourcesInGroup(pollCtx, logger, deleter, resourceGroupName)
		if err == nil {
			return true, nil
		}

		var nre nonRetriableError
		if errors.As(err, &nre) {
			return false, err
		}

		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 429 {
			logger.Info("Azure API throttling detected, will retry", "error", err.Error())
		} else {
			logger.Info("Error during cluster resource cleanup, will retry", "error", err.Error())
		}
		return false, nil
	})

	if pollErr != nil {
		if errors.Is(pollErr, context.DeadlineExceeded) {
			// Use the parent ctx (not infraCtx) since infraCtx is the one that timed out.
			listCtx, listCancel := context.WithTimeout(ctx, 30*time.Second)
			defer listCancel()
			remaining, listErr := deleter.ListByResourceGroup(listCtx, resourceGroupName)
			if listErr == nil {
				for _, r := range remaining {
					if o.isClusterResource(r.name) {
						logger.Info("Cluster resource still exists after timeout", "resource-id", r.id, "resource-type", r.resourceType)
					}
				}
			}
			return fmt.Errorf("timed out after %s waiting for cluster resource cleanup in resource group %s: %w", gracePeriod, resourceGroupName, pollErr)
		}
		return pollErr
	}
	return nil
}

func (o *DestroyInfraOptions) deleteClusterResourcesInGroup(ctx context.Context, logger logr.Logger, deleter resourceDeleter, resourceGroupName string) error {
	allResources, err := deleter.ListByResourceGroup(ctx, resourceGroupName)
	if err != nil {
		return err
	}

	var resourcesToDelete []resourceToDelete
	for _, resource := range allResources {
		if o.isClusterResource(resource.name) {
			resourcesToDelete = append(resourcesToDelete, resource)
			logger.Info("Marking cluster resource for deletion", "resource", resource.name, "id", resource.id, "type", resource.resourceType)
		} else {
			logger.Info("Preserving non-cluster resource", "resource", resource.name)
		}
	}

	sortResourcesByDeletionOrder(resourcesToDelete)

	var deletionErrors []error
	successfulDeletions := 0

	for _, resource := range resourcesToDelete {
		logger.Info("Deleting cluster resource", "resource-id", resource.id, "resource-type", resource.resourceType)
		if err := deleter.DeleteByID(ctx, resource.id, resource.apiVersion); err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 404 {
				logger.Info("Resource not found, skipping", "resource-id", resource.id)
				continue
			}
			logger.Error(err, "Failed to delete resource, continuing with remaining resources", "resource-id", resource.id, "resource-name", resource.name)
			deletionErrors = append(deletionErrors, fmt.Errorf("failed to delete resource %s (%s): %w", resource.name, resource.id, err))
			continue
		}
		logger.Info("Successfully deleted cluster resource", "resource-id", resource.id)
		successfulDeletions++
	}

	logger.Info("Cluster resource cleanup summary", "resources-deleted", successfulDeletions, "total-resources", len(resourcesToDelete), "errors", len(deletionErrors))

	if len(deletionErrors) > 0 {
		// If any deletion returns a NonRetriable error (e.g., auth/permission failure),
		// errors.As in the retry loop will match it in the joined error and stop retrying.
		// This is correct because such errors typically affect all resources equally.
		// A resource-specific NonRetriable error would also stop retries for other resources,
		// which is an acceptable trade-off to avoid masking systemic failures.
		return errors.Join(deletionErrors...)
	}

	return nil
}

// sortResourcesByDeletionOrder sorts resources so that child/dependent resources are deleted
// before their parents. The deletion order is:
// 1. Virtual network links (child of DNS zones)
// 2. Virtual machines
// 3. Network interfaces
// 4. Load balancers
// 5. Public IP addresses
// 6. Disks
// 7. Subnets (child of virtual networks)
// 8. Network security groups
// 9. Virtual networks
// 10. Private DNS zones
// 11. Storage accounts
// 12. Managed identities
// 13. Everything else
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
		case strings.Contains(resourceType, "subnets"):
			return 7
		case strings.Contains(resourceType, "networkSecurityGroups"):
			return 8
		case strings.Contains(resourceType, "virtualNetworks") && !strings.Contains(resourceType, "virtualNetworkLinks"):
			return 9
		case strings.Contains(resourceType, "privateDnsZones") && !strings.Contains(resourceType, "virtualNetworkLinks"):
			return 10
		case strings.Contains(resourceType, "storageAccounts"):
			return 11
		case strings.Contains(resourceType, "userAssignedIdentities"):
			return 12
		default:
			return 99
		}
	}

	slices.SortFunc(resources, func(a, b resourceToDelete) int {
		return priority(a.resourceType) - priority(b.resourceType)
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
		"Microsoft.Network/virtualNetworks/subnets":             "2023-11-01",
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
	return fallbackAzureResourceAPIVersion
}

// getResourceGroupName returns the resource group name to use for destroy operations.
// If a custom resource group name was provided, it is returned; otherwise, the default
// name format of {name}-{infraID} is used.
func (o *DestroyInfraOptions) getResourceGroupName() string {
	if len(o.ResourceGroupName) > 0 {
		return o.ResourceGroupName
	}
	return o.Name + "-" + o.InfraID
}
