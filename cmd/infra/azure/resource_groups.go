package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"k8s.io/utils/ptr"
)

// ResourceGroupManager handles Azure resource group operations
type ResourceGroupManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
}

// NewResourceGroupManager creates a new ResourceGroupManager
func NewResourceGroupManager(subscriptionID string, creds azcore.TokenCredential) *ResourceGroupManager {
	return &ResourceGroupManager{
		subscriptionID: subscriptionID,
		creds:          creds,
	}
}

// CreateOrGetResourceGroup creates the three resource groups needed for the cluster:
// 1. The resource group for the cluster's infrastructure
// 2. The resource group for the virtual network
// 3. The resource group for the network security group
func (r *ResourceGroupManager) CreateOrGetResourceGroup(ctx context.Context, opts *CreateInfraOptions, rgName string) (string, string, error) {
	existingRGSuccessMsg := "Successfully found existing resource group"
	createdRGSuccessMsg := "Successfully created resource group"

	resourceGroupClient, err := armresources.NewResourceGroupsClient(r.subscriptionID, r.creds, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	// Use a provided resource group if it was provided
	if opts.ResourceGroupName != "" {
		response, err := resourceGroupClient.Get(ctx, opts.ResourceGroupName, nil)
		if err != nil {
			return "", "", fmt.Errorf("failed to get resource group name, '%s': %w", opts.ResourceGroupName, err)
		}

		return *response.Name, existingRGSuccessMsg, nil
	}

	resourceGroupTags := map[string]*string{}
	for key, value := range opts.ResourceGroupTags {
		resourceGroupTags[key] = ptr.To(value)
	}

	// Create a resource group since none was provided
	resourceGroupName := opts.Name + "-" + opts.InfraID
	if rgName != "" {
		resourceGroupName = rgName + "-" + opts.InfraID
	}
	parameters := armresources.ResourceGroup{
		Location: ptr.To(opts.Location),
		Tags:     resourceGroupTags,
	}
	response, err := resourceGroupClient.CreateOrUpdate(ctx, resourceGroupName, parameters, nil)
	if err != nil {
		return "", "", fmt.Errorf("createResourceGroup: failed to create a resource group: %w", err)
	}

	return *response.Name, createdRGSuccessMsg, nil
}