package azure

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type DestroyInfraOptions struct {
	Name               string
	Location           string
	InfraID            string
	CredentialsFile    string
	Credentials        *util.AzureCreds
	ResourceGroupName  string
	TechPreviewEnabled bool
	ControlPlaneMIs    hyperv1.AzureResourceManagedIdentities
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

	if o.TechPreviewEnabled {
		// Destroy created service principals
		logger.Info("Destroying service principals")
		err = destroyServicePrincipals(o)
		if err != nil {
			logger.Error(err, "Failed to destroy service principals")
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

func destroyServicePrincipals(o *DestroyInfraOptions) error {
	clientID := o.ControlPlaneMIs.ControlPlane.ControlPlaneOperator.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.ImageRegistry.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.NodePoolManagement.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.CloudProvider.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.Network.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.Ingress.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.Disk.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}
	clientID = o.ControlPlaneMIs.ControlPlane.File.ClientID
	if err := destroyServicePrincipal(clientID); err != nil {
		return err
	}

	return nil
}

func destroyServicePrincipal(clientID string) error {
	cmdStr := `az ad app delete --id ` + clientID
	cmd := exec.Command("sh", "-c", cmdStr)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete service principal, %s: %w", clientID, err)
	}

	return nil
}
