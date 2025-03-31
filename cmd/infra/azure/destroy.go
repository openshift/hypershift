package azure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	azureauth "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type DestroyInfraOptions struct {
	Name                        string
	Location                    string
	InfraID                     string
	CredentialsFile             string
	Credentials                 *util.AzureCreds
	ResourceGroupName           string
	VnetID                      string
	NsgID                       string
	DNSZoneResourceGroupName    string
	CPOManagedIdentityClientIDs map[string]string
	UsedCustomHCPRoles          bool
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

	// Delete Role Assignments
	if o.VnetID != "" && o.NsgID != "" {
		roleAssignmentClient, err := azureauth.NewRoleAssignmentsClient(subscriptionID, azureCreds, nil)
		if err != nil {
			return fmt.Errorf("failed to create new role assignments client in deleting role assignments: %w", err)
		}
		_, vnetResourceGroupName, err := azureutil.GetVnetNameAndResourceGroupFromVnetID(o.VnetID)
		if err != nil {
			return fmt.Errorf("failed to get vnet resource group from vnet ID to delete role assignment: %w", err)
		}
		_, nsgResourceGroupName, err := azureutil.GetNameAndResourceGroupFromNetworkSecurityGroupID(o.NsgID)
		if err != nil {
			return fmt.Errorf("failed to get nsg resource group from nsg ID to delete role assignment: %w", err)
		}

		for component := range o.CPOManagedIdentityClientIDs {
			logger.Info("Information for deleting role assignment", "resource-group", o.ResourceGroupName, "vnet-resource-group", vnetResourceGroupName, "nsg-resource-group", nsgResourceGroupName, "DNS-zone-resource-group", o.DNSZoneResourceGroupName)
			_, scopes := azureutil.GetServicePrincipalScopes(subscriptionID, o.ResourceGroupName, nsgResourceGroupName, vnetResourceGroupName, o.DNSZoneResourceGroupName, component, o.UsedCustomHCPRoles)

			for _, scope := range scopes {
				// Generate the role assignment name
				roleAssignmentName := util.GenerateRoleAssignmentName(o.InfraID, component, scope)

				logger.Info("Scope for role assignment", "scope", scope)
				_, err = roleAssignmentClient.Delete(ctx, scope, roleAssignmentName, nil)
				if err != nil {
					logger.Error(err, "failed to delete role assignment", "roleAssignmentName", roleAssignmentName, "component", component, "scope", scope)
					return fmt.Errorf("[%s] failed to delete role assignment, %s, component, %s, scope, %s: %w", time.Now().Format("2006-01-02 15:04:05"), roleAssignmentName, component, scope, err)
				}
				logger.Info("Role assignment successfully deleted", "component", component, "scope", scope)
			}
		}
	}

	// Delete the resource group
	var resourceGroups []string
	resourceGroups = append(resourceGroups, o.GetResourceGroupName())

	for _, rg := range additionalResourceGroups {
		exists, err := resourceGroupClient.CheckExistence(ctx, rg, nil)
		if err != nil {
			return fmt.Errorf("failed to check existence of resource group %s: %w", rg, err)
		}
		if exists.Success {
			resourceGroups = append(resourceGroups, rg)
		}
	}

	for _, rg := range resourceGroups {
		logger.Info("Deleting resource group", "resource-group", rg)
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

	return nil
}

func (o *DestroyInfraOptions) GetResourceGroupName() string {
	if len(o.ResourceGroupName) > 0 {
		return o.ResourceGroupName
	}
	return o.Name + "-" + o.InfraID
}
