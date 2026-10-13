package ibmcloud

import (
	"github.com/IBM/platform-services-go-sdk/resourcemanagerv2"
	"github.com/openshift/cloud-credential-operator/pkg/ibmcloud"
	"github.com/pkg/errors"
)

// getResourceGroupID returns the resource group ID associated with the given name, returns nil if name is empty.
func getResourceGroupID(client ibmcloud.Client, accountID *string, name string) (string, error) {
	if name == "" {
		return "", nil
	}

	// Get the ID for the given resourceGroupName
	listResourceGroupsOptions := &resourcemanagerv2.ListResourceGroupsOptions{
		AccountID: accountID,
		Name:      &name,
	}
	resourceGroups, _, err := client.ListResourceGroups(listResourceGroupsOptions)
	if err != nil {
		return "", errors.Wrapf(err, "Failed to list resource groups for the name: %s", name)
	}

	if len(resourceGroups.Resources) == 0 {
		return "", errors.Errorf("Resource group %s not found", name)
	}

	return *resourceGroups.Resources[0].ID, nil
}
