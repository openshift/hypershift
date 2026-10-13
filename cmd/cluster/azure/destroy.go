package azure

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/spf13/cobra"
)

const (
	defaultClusterGracePeriod = 10 * time.Minute
	privateClusterGracePeriod = 20 * time.Minute
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = config.DefaultAzureLocation
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, "The name of the resource group containing the HostedCluster infrastructure resources that need to be destroyed.")
	cmd.Flags().BoolVar(&opts.AzurePlatform.PreserveResourceGroup, "preserve-resource-group", opts.AzurePlatform.PreserveResourceGroup, "When true, the managed/main resource group will not be deleted during cluster destroy. Only cluster-specific resources within the resource group will be cleaned up.")
	cmd.Flags().StringVar(&opts.AzurePlatform.DNSZoneRGName, "dns-zone-rg-name", opts.AzurePlatform.DNSZoneRGName, util.DNSZoneRGNameDestroyDescription)

	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("dns-zone-rg-name")

	logger := log.Log
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := DestroyCluster(ctx, opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func applyHostedClusterToDestroyOptions(o *core.DestroyOptions, hostedCluster *hyperv1.HostedCluster) error {
	if hostedCluster == nil {
		if o.AzurePlatform.Cloud == "" {
			o.AzurePlatform.Cloud = config.DefaultAzureCloud
		}
		return nil
	}
	if hostedCluster.Spec.Platform.Azure == nil {
		return fmt.Errorf("hostedcluster %s/%s is not an Azure platform cluster", hostedCluster.Namespace, hostedCluster.Name)
	}
	azureSpec := hostedCluster.Spec.Platform.Azure
	o.InfraID = hostedCluster.Spec.InfraID
	o.AzurePlatform.Location = azureSpec.Location
	o.AzurePlatform.Cloud = config.DefaultAzureCloud
	if azureSpec.Cloud != "" {
		o.AzurePlatform.Cloud = azureSpec.Cloud
	}

	// Increase grace period for private topology clusters which require more
	// cleanup time (Private Link Services, DNS zones, VNet links, etc).
	// Only override if the user hasn't explicitly set a custom value.
	topology := azureSpec.Topology
	if (topology == hyperv1.AzureTopologyPrivate || topology == hyperv1.AzureTopologyPublicAndPrivate) &&
		o.ClusterGracePeriod == defaultClusterGracePeriod {
		o.ClusterGracePeriod = privateClusterGracePeriod
	}
	return nil
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}

	if err := applyHostedClusterToDestroyOptions(o, hostedCluster); err != nil {
		return err
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if o.AzurePlatform.Location == "" {
		inputErrors = append(inputErrors, fmt.Errorf("location is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	// Verify a user provided resource group name is correct by trying to retrieve it before carrying on with deleting things
	if o.AzurePlatform.ResourceGroupName != "" {
		// Setup subscription ID and Azure credential information
		subscriptionID, azureCreds, err := util.SetupAzureCredentials(o.Log, nil, o.AzurePlatform.CredentialsFile)
		if err != nil {
			return fmt.Errorf("failed to setup Azure credentials: %w", err)
		}

		// Setup cloud configuration
		cloudConfig, err := azureutil.GetAzureCloudConfiguration(o.AzurePlatform.Cloud)
		if err != nil {
			return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
		}
		clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

		// Setup Azure resource group client
		resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, clientOptions)
		if err != nil {
			return fmt.Errorf("failed to create new resource groups client: %w", err)
		}

		if _, err = resourceGroupClient.Get(ctx, o.AzurePlatform.ResourceGroupName, nil); err != nil {
			return fmt.Errorf("failed to get resource group name, '%s': %w", o.AzurePlatform.ResourceGroupName, err)
		}
	} else {
		o.AzurePlatform.ResourceGroupName = o.Name + "-" + o.InfraID
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	// Clean up role assignments before destroying infrastructure to avoid orphans.
	// Match the create path resource-group names: {name}-nsg and {name}-vnet.
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(o.Log, nil, o.AzurePlatform.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	nsgRG := o.Name + "-nsg"
	vnetRG := o.Name + "-vnet"

	rbacManager := azureinfra.NewRBACManager(subscriptionID, azureCreds)
	// assignCustomHCPRoles=false is safe: GetServicePrincipalScopes only uses the flag to select
	// the role definition ID, not to modify the scopes list. Cleanup derives role assignment names
	// from infraID + component + scope, so the role ID is irrelevant.
	if err := rbacManager.CleanupRoleAssignments(ctx, o.Log, o.InfraID, o.AzurePlatform.ResourceGroupName, nsgRG, vnetRG, o.AzurePlatform.DNSZoneRGName, false); err != nil {
		o.Log.Error(err, "Failed to clean up some role assignments, continuing with infrastructure deletion")
	}

	destroyInfraOptions := &azureinfra.DestroyInfraOptions{
		Name:                  o.Name,
		Location:              o.AzurePlatform.Location,
		InfraID:               o.InfraID,
		CredentialsFile:       o.AzurePlatform.CredentialsFile,
		Cloud:                 o.AzurePlatform.Cloud,
		ResourceGroupName:     o.AzurePlatform.ResourceGroupName,
		PreserveResourceGroup: o.AzurePlatform.PreserveResourceGroup,
	}
	return destroyInfraOptions.Run(ctx, o.Log)
}
