package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	azureauth "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/pageblob"

	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const (
	VirtualNetworkAddressPrefix       = "10.0.0.0/16"
	VirtualNetworkLinkLocation        = "global"
	VirtualNetworkSubnetAddressPrefix = "10.0.0.0/24"

	graphAPIEndpoint = "https://graph.microsoft.com/v1.0/servicePrincipals"
)

type CreateInfraOptions struct {
	Name                        string
	BaseDomain                  string
	Location                    string
	InfraID                     string
	CredentialsFile             string
	Credentials                 *util.AzureCreds
	OutputFile                  string
	RHCOSImage                  string
	ResourceGroupName           string
	VnetID                      string
	NetworkSecurityGroupID      string
	ResourceGroupTags           map[string]string
	SubnetID                    string
	ManagedIdentitiesFile       string
	DataPlaneIdentitiesFile     string
	AssignServicePrincipalRoles bool
	DNSZoneRG                   string
	AssignCustomHCPRoles        bool
	DisableClusterCapabilities  []string
}

type CreateInfraOutput struct {
	BaseDomain          string                                  `json:"baseDomain"`
	PublicZoneID        string                                  `json:"publicZoneID"`
	PrivateZoneID       string                                  `json:"privateZoneID"`
	Location            string                                  `json:"region"`
	ResourceGroupName   string                                  `json:"resourceGroupName"`
	VNetID              string                                  `json:"vnetID"`
	SubnetID            string                                  `json:"subnetID"`
	BootImageID         string                                  `json:"bootImageID"`
	InfraID             string                                  `json:"infraID"`
	SecurityGroupID     string                                  `json:"securityGroupID"`
	ControlPlaneMIs     *hyperv1.AzureResourceManagedIdentities `json:"controlPlaneMIs"`
	DataPlaneIdentities hyperv1.DataPlaneManagedIdentities      `json:"dataPlaneIdentities"`
}

type ServicePrincipalResponse struct {
	Value []struct {
		ID string `json:"id"`
	} `json:"value"`
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates Azure infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		Location: "eastus",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID(required)")
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to a credentials file (required)")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location where cluster infra should be created")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, "The Network Security Group ID to use in the default NodePool.")
	cmd.Flags().StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The subnet ID where the VMs will be placed.")
	cmd.Flags().StringVar(&opts.RHCOSImage, "rhcos-image", opts.RHCOSImage, `RHCOS image to be used for the NodePool. Could be obtained using podman run --rm -it --entrypoint cat $RELEASE_IMAGE release-manifests/0000_50_installer_coreos-bootimages.yaml | yq .data.stream -r | yq '.architectures.x86_64["rhel-coreos-extensions"]["azure-disk"].url'`)
	cmd.Flags().StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, "Additional tags to apply to the resource group created (e.g. 'key1=value1,key2=value2')")

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if _, err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create infrastructure")
			return err
		}
		l.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}

func (o *CreateInfraOptions) Run(ctx context.Context, l logr.Logger) (*CreateInfraOutput, error) {
	result := CreateInfraOutput{
		Location:   o.Location,
		InfraID:    o.InfraID,
		BaseDomain: o.BaseDomain,
	}

	// Setup subscription ID and Azure credential information
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(l, o.Credentials, o.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	// Create an Azure resource group
	resourceGroupName, msg, err := createResourceGroup(ctx, o, azureCreds, "", subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to create a resource group: %w", err)
	}
	result.ResourceGroupName = resourceGroupName
	l.Info(msg, "name", resourceGroupName)

	// Capture the base DNS zone's resource group's ID
	result.PublicZoneID, err = getBaseDomainID(ctx, subscriptionID, azureCreds, o.BaseDomain)
	if err != nil {
		return nil, err
	}

	// Set the network security group ID either from the flag value or create one
	nsgResourceGroupName := ""
	if len(o.NetworkSecurityGroupID) > 0 {
		result.SecurityGroupID = o.NetworkSecurityGroupID

		// We need to get the resource group name for creating the service principals
		_, nsgResourceGroupName, err = azureutil.GetNameAndResourceGroupFromNetworkSecurityGroupID(o.NetworkSecurityGroupID)
		if err != nil {
			return nil, err
		}

		l.Info("Using existing network security group", "ID", result.SecurityGroupID)
	} else {
		// Create a resource group for network security group
		nsgResourceGroupName = o.Name + "-nsg"
		nsgResourceGroupName, msg, err = createResourceGroup(ctx, o, azureCreds, nsgResourceGroupName, subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource group for network security group: %w", err)
		}
		l.Info(msg, "name", nsgResourceGroupName)

		// Create a network security group
		nsgID, err := createSecurityGroup(ctx, subscriptionID, nsgResourceGroupName, o.Name, o.InfraID, o.Location, azureCreds)
		if err != nil {
			return nil, err
		}
		result.SecurityGroupID = nsgID
		l.Info("Successfully created network security group", "ID", result.SecurityGroupID)
	}

	// Set the subnet ID from the flag value
	if len(o.SubnetID) > 0 {
		result.SubnetID = o.SubnetID
		l.Info("Using existing subnet", "ID", result.SubnetID)
	}

	// Retrieve a client's existing virtual network if a VNET ID was provided; otherwise, create a new one
	vnetResourceGroupName := ""
	if len(o.VnetID) > 0 {
		result.VNetID = o.VnetID

		// We need to get the resource group name for creating the service principals
		_, vnetResourceGroupName, err = azureutil.GetVnetNameAndResourceGroupFromVnetID(o.VnetID)
		if err != nil {
			return nil, err
		}

		l.Info("Using existing vnet", "ID", result.VNetID)
	} else {
		//create a resource group for virtual network
		vnetResourceGroupName = o.Name + "-vnet"
		vnetResourceGroupName, msg, err = createResourceGroup(ctx, o, azureCreds, vnetResourceGroupName, subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource group for virtual network: %w", err)
		}
		l.Info(msg, "name", vnetResourceGroupName)

		// Create a virtual network
		vnet, err := createVirtualNetwork(ctx, subscriptionID, vnetResourceGroupName, o.Name, o.InfraID, o.Location, o.SubnetID, result.SecurityGroupID, azureCreds)
		if err != nil {
			return nil, err
		}
		result.SubnetID = *vnet.Properties.Subnets[0].ID
		result.VNetID = *vnet.ID
		l.Info("Successfully created vnet", "ID", result.VNetID)
	}

	if o.ManagedIdentitiesFile != "" {
		result.ControlPlaneMIs = &hyperv1.AzureResourceManagedIdentities{}

		managedIdentitiesRaw, err := os.ReadFile(o.ManagedIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --managed-identities-file %s: %w", o.ManagedIdentitiesFile, err)
		}
		if err := yaml.Unmarshal(managedIdentitiesRaw, &result.ControlPlaneMIs.ControlPlane); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --managed-identities-file: %w", err)
		}

		components := map[string]hyperv1.AzureClientID{
			config.CPO:           result.ControlPlaneMIs.ControlPlane.ControlPlaneOperator.ClientID,
			config.NodePoolMgmt:  result.ControlPlaneMIs.ControlPlane.NodePoolManagement.ClientID,
			config.CloudProvider: result.ControlPlaneMIs.ControlPlane.CloudProvider.ClientID,
			config.AzureFile:     result.ControlPlaneMIs.ControlPlane.File.ClientID,
			config.AzureDisk:     result.ControlPlaneMIs.ControlPlane.Disk.ClientID,
			config.Ingress:       result.ControlPlaneMIs.ControlPlane.Ingress.ClientID,
			config.CNCC:          result.ControlPlaneMIs.ControlPlane.Network.ClientID,
		}

		if !slices.Contains(o.DisableClusterCapabilities, string(hyperv1.ImageRegistryCapability)) {
			components[config.CIRO] = result.ControlPlaneMIs.ControlPlane.ImageRegistry.ClientID
		}

		// Perform role assignments over each component's service principal
		if o.AssignServicePrincipalRoles {
			// Get an access token for Microsoft Graph API for getting the object IDs
			token, err := getAzureToken(azureCreds)
			if err != nil {
				return nil, err
			}

			for component, clientID := range components {
				objectID, err := getObjectIDFromClientID(string(clientID), token)
				if err != nil {
					return nil, err
				}

				role, scopes := azureutil.GetServicePrincipalScopes(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, o.DNSZoneRG, component, o.AssignCustomHCPRoles)

				// For each resource group (aka scope), assign the role to the service principal
				for _, scope := range scopes {
					if err := assignRole(ctx, subscriptionID, o.InfraID, component, objectID, role, scope, azureCreds); err != nil {
						return nil, fmt.Errorf("failed to perform role assignment: %w", err)
					}
				}
			}
		}
	}

	if o.DataPlaneIdentitiesFile != "" {
		managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroupName)

		dataPlaneIdentitiesRaw, err := os.ReadFile(o.DataPlaneIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --data-plane-identities-file %s: %w", o.DataPlaneIdentitiesFile, err)
		}
		if err := yaml.Unmarshal(dataPlaneIdentitiesRaw, &result.DataPlaneIdentities); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --data-plane-identities-file: %w", err)
		}

		if o.AssignServicePrincipalRoles {
			// Get an access token for Microsoft Graph API for getting the object IDs
			token, err := getAzureToken(azureCreds)
			if err != nil {
				return nil, err
			}

			// Setup Data Plane MI role assignments
			objectID, err := getObjectIDFromClientID(result.DataPlaneIdentities.ImageRegistryMSIClientID, token)
			if err != nil {
				return nil, err
			}
			err = assignRole(ctx, subscriptionID, o.InfraID, config.CIRO+"WI", objectID, config.ImageRegistryRoleDefinitionID, managedRG, azureCreds)
			if err != nil {
				return nil, err
			}

			objectID, err = getObjectIDFromClientID(result.DataPlaneIdentities.DiskMSIClientID, token)
			if err != nil {
				return nil, err
			}
			err = assignRole(ctx, subscriptionID, o.InfraID, config.AzureDisk+"WI", objectID, config.AzureDiskRoleDefinitionID, managedRG, azureCreds)
			if err != nil {
				return nil, err
			}

			objectID, err = getObjectIDFromClientID(result.DataPlaneIdentities.FileMSIClientID, token)
			if err != nil {
				return nil, err
			}
			err = assignRole(ctx, subscriptionID, o.InfraID, config.AzureFile+"WI", objectID, config.AzureFileRoleDefinitionID, managedRG, azureCreds)
			if err != nil {
				return nil, err
			}
		}
	}

	// Create private DNS zone
	privateDNSZoneID, privateDNSZoneName, err := createPrivateDNSZone(ctx, subscriptionID, resourceGroupName, o.Name, o.BaseDomain, azureCreds)
	if err != nil {
		return nil, err
	}
	result.PrivateZoneID = privateDNSZoneID
	l.Info("Successfully created private DNS zone", "name", privateDNSZoneName)

	// Create private DNS zone link
	err = createPrivateDNSZoneLink(ctx, subscriptionID, resourceGroupName, o.Name, o.InfraID, result.VNetID, privateDNSZoneName, azureCreds)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created private DNS zone link")

	// Create a public IP address for the egress load balancer
	publicIPAddress, err := createPublicIPAddressForLB(ctx, subscriptionID, resourceGroupName, o.InfraID, o.Location, azureCreds)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created public IP address for guest cluster egress load balancer")

	// Create a load balancer for guest cluster egress
	err = createLoadBalancer(ctx, subscriptionID, resourceGroupName, o.InfraID, o.Location, publicIPAddress, azureCreds)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created guest cluster egress load balancer")

	// This is only populated if an Azure Marketplace image wasn't provided.
	// If one wasn't provided, a boot image ID needs to be created.
	if o.RHCOSImage != "" {
		// Upload RHCOS image and create a bootable image
		result.BootImageID, err = createRhcosImages(ctx, l, o, subscriptionID, resourceGroupName, azureCreds)
		if err != nil {
			return nil, fmt.Errorf("failed to create RHCOS image: %w", err)
		}
	}

	if o.OutputFile != "" {
		resultSerialized, err := yaml.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize result: %w", err)
		}
		if err := os.WriteFile(o.OutputFile, resultSerialized, 0644); err != nil {
			// Be nice and print the data, so it doesn't get lost
			l.Error(err, "Writing output file failed", "Output File", o.OutputFile, "data", string(resultSerialized))
			return nil, fmt.Errorf("failed to write result to --output-file: %w", err)
		}
	}

	return &result, nil

}

// createResourceGroup creates the three resource groups needed for the cluster
// 1. The resource group for the cluster's infrastructure
// 2. The resource group for the virtual network
// 3. The resource group for the network security group
func createResourceGroup(ctx context.Context, o *CreateInfraOptions, azureCreds azcore.TokenCredential, rgName, subscriptionID string) (string, string, error) {
	existingRGSuccessMsg := "Successfully found existing resource group"
	createdRGSuccessMsg := "Successfully created resource group"

	resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create new resource groups client: %w", err)
	}

	// Use a provided resource group if it was provided
	if o.ResourceGroupName != "" && rgName == "" {
		response, err := resourceGroupClient.Get(ctx, o.ResourceGroupName, nil)
		if err != nil {
			return "", "", fmt.Errorf("failed to get resource group name, '%s': %w", o.ResourceGroupName, err)
		}

		return *response.Name, existingRGSuccessMsg, nil
	} else {

		resourceGroupTags := map[string]*string{}
		for key, value := range o.ResourceGroupTags {
			resourceGroupTags[key] = ptr.To(value)
		}

		// Create a resource group since none was provided
		resourceGroupName := o.Name + "-" + o.InfraID
		if rgName != "" {
			resourceGroupName = rgName + "-" + o.InfraID
		}
		parameters := armresources.ResourceGroup{
			Location: ptr.To(o.Location),
			Tags:     resourceGroupTags,
		}
		response, err := resourceGroupClient.CreateOrUpdate(ctx, resourceGroupName, parameters, nil)
		if err != nil {
			return "", "", fmt.Errorf("createResourceGroup: failed to create a resource group: %w", err)
		}

		return *response.Name, createdRGSuccessMsg, nil
	}
}

// getBaseDomainID gets the resource group ID for the resource group containing the base domain
func getBaseDomainID(ctx context.Context, subscriptionID string, azureCreds azcore.TokenCredential, baseDomain string) (string, error) {
	zonesClient, err := armdns.NewZonesClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create dns zone %s: %w", baseDomain, err)
	}

	pager := zonesClient.NewListPager(nil)
	if pager.More() {
		pagerResults, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve list of DNS zones: %w", err)
		}

		for _, result := range pagerResults.Value {
			if *result.Name == baseDomain {
				return *result.ID, nil
			}
		}
	}
	return "", fmt.Errorf("could not find any DNS zones in subscription")
}

// createSecurityGroup creates the security group the virtual network will use
func createSecurityGroup(ctx context.Context, subscriptionID string, resourceGroupName string, name string, infraID string, location string, azureCreds azcore.TokenCredential) (string, error) {
	securityGroupClient, err := armnetwork.NewSecurityGroupsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create security group client: %w", err)
	}
	securityGroupFuture, err := securityGroupClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-"+infraID+"-nsg", armnetwork.SecurityGroup{Location: &location}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create network security group: %w", err)
	}
	securityGroup, err := securityGroupFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get network security group creation result: %w", err)
	}

	return *securityGroup.ID, nil
}

// createVirtualNetwork creates the virtual network
func createVirtualNetwork(ctx context.Context, subscriptionID string, resourceGroupName string, name string, infraID string, location string, subnetID string, securityGroupID string, azureCreds azcore.TokenCredential) (armnetwork.VirtualNetworksClientCreateOrUpdateResponse, error) {
	l := ctrl.LoggerFrom(ctx)

	networksClient, err := armnetwork.NewVirtualNetworksClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create new virtual networks client: %w", err)
	}

	vnetToCreate := armnetwork.VirtualNetwork{
		Location: &location,
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{
					ptr.To(VirtualNetworkAddressPrefix),
				},
			},
			Subnets: []*armnetwork.Subnet{},
		},
	}

	if len(subnetID) > 0 {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{ID: ptr.To(subnetID)})
		l.Info("Using existing subnet in vnet creation", "ID", subnetID)
	} else {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{
			Name: ptr.To("default"),
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: ptr.To(VirtualNetworkSubnetAddressPrefix),
				NetworkSecurityGroup: &armnetwork.SecurityGroup{
					ID: ptr.To(securityGroupID),
				},
			},
		})
		l.Info("Creating new subnet for vnet creation")
	}

	vnetFuture, err := networksClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-"+infraID, vnetToCreate, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create vnet: %w", err)
	}
	vnet, err := vnetFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to wait for vnet creation: %w", err)
	}

	if vnet.ID == nil || vnet.Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no ID or name")
	}

	if len(vnet.Properties.Subnets) < 1 {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnets: %+v", vnet)
	}

	if vnet.Properties.Subnets[0].ID == nil || vnet.Properties.Subnets[0].Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnet ID or name")
	}

	return vnet, nil
}

// createPrivateDNSZone creates the private DNS zone
func createPrivateDNSZone(ctx context.Context, subscriptionID string, resourceGroupName string, name string, baseDomain string, azureCreds azcore.TokenCredential) (string, string, error) {
	privateZoneClient, err := armprivatedns.NewPrivateZonesClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create new private zones client: %w", err)
	}
	privateZoneParams := armprivatedns.PrivateZone{
		Location: ptr.To("global"),
	}
	privateDNSZonePromise, err := privateZoneClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-azurecluster."+baseDomain, privateZoneParams, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create private DNS zone: %w", err)
	}
	privateDNSZone, err := privateDNSZonePromise.PollUntilDone(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed waiting for private DNS zone completion: %w", err)
	}

	return *privateDNSZone.ID, *privateDNSZone.Name, nil
}

// createPrivateDNSZoneLink creates the private DNS Zone network link
func createPrivateDNSZoneLink(ctx context.Context, subscriptionID string, resourceGroupName string, name string, infraID string, vnetID string, privateDNSZoneName string, azureCreds azcore.TokenCredential) error {
	privateZoneLinkClient, err := armprivatedns.NewVirtualNetworkLinksClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new virtual network links client: %w", err)
	}

	virtualNetworkLinkParams := armprivatedns.VirtualNetworkLink{
		Location: ptr.To(VirtualNetworkLinkLocation),
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork:      &armprivatedns.SubResource{ID: &vnetID},
			RegistrationEnabled: ptr.To(false),
		},
	}
	networkLinkPromise, err := privateZoneLinkClient.BeginCreateOrUpdate(ctx, resourceGroupName, privateDNSZoneName, name+"-"+infraID, virtualNetworkLinkParams, nil)
	if err != nil {
		return fmt.Errorf("failed to set up network link for private DNS zone: %w", err)
	}
	_, err = networkLinkPromise.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting for network link for private DNS zone: %w", err)
	}

	return nil
}

// createRhcosImages uploads the RHCOS image and creates a bootable image
func createRhcosImages(ctx context.Context, l logr.Logger, o *CreateInfraOptions, subscriptionID string, resourceGroupName string, azureCreds azcore.TokenCredential) (string, error) {
	storageAccountClient, err := armstorage.NewAccountsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new accounts client for storage: %w", err)
	}

	storageAccountName := "cluster" + utilrand.String(5)
	storageAccountFuture, err := storageAccountClient.BeginCreate(ctx, resourceGroupName, storageAccountName,
		armstorage.AccountCreateParameters{
			SKU: &armstorage.SKU{
				Name: ptr.To(armstorage.SKUNamePremiumLRS),
				Tier: ptr.To(armstorage.SKUTierStandard),
			},
			Location: ptr.To(o.Location),
		}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create storage account: %w", err)
	}
	storageAccount, err := storageAccountFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed waiting for storage account creation to complete: %w", err)
	}
	l.Info("Successfully created storage account", "name", *storageAccount.Name)

	blobContainersClient, err := armstorage.NewBlobContainersClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create blob containers client: %w", err)
	}

	imageContainer, err := blobContainersClient.Create(ctx, resourceGroupName, storageAccountName, "vhd", armstorage.BlobContainer{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create blob container: %w", err)
	}
	l.Info("Successfully created blob container", "name", *imageContainer.Name)

	sourceURL := o.RHCOSImage
	blobName := "rhcos.x86_64.vhd"

	// Explicitly check this, Azure API makes inferring the problem from the error message extremely hard
	if !strings.HasPrefix(sourceURL, "https://rhcos.blob.core.windows.net") {
		return "", fmt.Errorf("the image source url must be from an azure blob storage, otherwise upload will fail with an `One of the request inputs is out of range` error")
	}

	// storage object access has its own authentication system: https://github.com/hashicorp/terraform-provider-azurerm/blob/b0c897055329438be6a3a159f6ffac4e1ce958f2/internal/services/storage/client/client.go#L133
	accountsClient, err := armstorage.NewAccountsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new accounts client: %w", err)
	}
	storageAccountKeyResult, err := accountsClient.ListKeys(ctx, resourceGroupName, storageAccountName, &armstorage.AccountsClientListKeysOptions{Expand: ptr.To("kerb")})
	if err != nil {
		return "", fmt.Errorf("failed to list storage account keys: %w", err)
	}
	if len(storageAccountKeyResult.Keys) == 0 || storageAccountKeyResult.Keys[0].Value == nil {
		return "", errors.New("no storage account keys exist")
	}

	credential, err := container.NewSharedKeyCredential(storageAccountName, *storageAccountKeyResult.Keys[0].Value)
	if err != nil {
		return "", fmt.Errorf("failed to create shared key credentials: %w", err)
	}

	imageBlobURLPrefix := fmt.Sprintf("https://%s.blob.core.windows.net/vhd/", storageAccountName)

	containerClient, err := container.NewClientWithSharedKeyCredential(imageBlobURLPrefix, credential, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new container client: %w", err)
	}

	// VHDs should be uploaded to page blobs instead of block blobs per
	// https://learn.microsoft.com/en-us/answers/questions/792044/how-to-create-a-vm-from-vhd-file-in-azure
	pageBlobClient := containerClient.NewPageBlobClient(blobName)
	_, err = pageBlobClient.Create(ctx, 0, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create page blob for vhd: %w", err)
	}

	l.Info("Copying RHCOS image to vhd blob, this can take a few minutes...")
	err = copyImageAndWait(ctx, sourceURL, pageBlobClient)
	if err != nil {
		return "", err
	}

	l.Info("Successfully uploaded RHCOS image to vhd blob")
	imagesClient, err := armcompute.NewImagesClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create images client: %w", err)
	}

	imageInput := armcompute.Image{
		Properties: &armcompute.ImageProperties{
			StorageProfile: &armcompute.ImageStorageProfile{
				OSDisk: &armcompute.ImageOSDisk{
					OSType:  ptr.To(armcompute.OperatingSystemTypesLinux),
					OSState: ptr.To(armcompute.OperatingSystemStateTypesGeneralized),
					BlobURI: ptr.To(imageBlobURLPrefix + blobName),
				},
			},
			HyperVGeneration: ptr.To(armcompute.HyperVGenerationTypesV1),
		},
		Location: ptr.To(o.Location),
	}
	imageCreationFuture, err := imagesClient.BeginCreateOrUpdate(ctx, resourceGroupName, blobName, imageInput, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create image: %w", err)
	}
	imageCreationResult, err := imageCreationFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to wait for image creation to finish: %w", err)
	}
	bootImageID := *imageCreationResult.ID
	l.Info("Successfully created image", "resourceID", *imageCreationResult.ID, "result", imageCreationResult)

	return bootImageID, nil
}

// copyImageAndWait copies an RHCOS image from its Azure blob URL to a page blob within the managed resource group to be
// used as the basis for creating Azure virtual machines for a NodePool.
//
// This function is hardcoded to wait 10 minutes for the copy to complete or else it will error out.
func copyImageAndWait(ctx context.Context, rhcosURL string, pageBlobClient *pageblob.Client) error {
	_, err := pageBlobClient.CopyFromURL(ctx, rhcosURL, nil)
	if err != nil {
		return fmt.Errorf("failed to start the process to copy rhcos image to vhd blob: %w", err)
	}

	if err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		// Grab the latest status on the copy effort
		properties, err := pageBlobClient.GetProperties(ctx, nil)
		if err != nil {
			return true, fmt.Errorf("failed to check rhcos copy status: %w", err)
		}

		// This should never happen but just in case
		if properties.CopyStatus == nil {
			return true, fmt.Errorf("rhcos copy status is nil")
		}

		// Copy is complete, bail out
		if *properties.CopyStatus == blob.CopyStatusTypeSuccess {
			return true, nil
		}

		// Something went wrong with the copy process, bail out
		if *properties.CopyStatus == blob.CopyStatusTypeAborted || *properties.CopyStatus == blob.CopyStatusTypeFailed {
			return true, fmt.Errorf("failed to copy rhcos image: %w", err)
		}

		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to copy and wait for rhcos image: %w", err)
	}

	return nil
}

// createPublicIPAddressForLB creates a public IP address to use for the outbound rule in the load balancer
func createPublicIPAddressForLB(ctx context.Context, subscriptionID string, resourceGroupName string, infraID string, location string, azureCreds azcore.TokenCredential) (*armnetwork.PublicIPAddress, error) {
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address client, %w", err)
	}

	pollerResp, err := publicIPAddressClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		infraID,
		armnetwork.PublicIPAddress{
			Name:     ptr.To(infraID),
			Location: ptr.To(location),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAddressVersion:   ptr.To(armnetwork.IPVersionIPv4),
				PublicIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodStatic),
				IdleTimeoutInMinutes:     ptr.To[int32](4),
			},
			SKU: &armnetwork.PublicIPAddressSKU{
				Name: ptr.To(armnetwork.PublicIPAddressSKUNameStandard),
			},
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address, %w", err)
	}

	resp, err := pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed while waiting create public IP address, %w", err)
	}
	return &resp.PublicIPAddress, nil
}

// createLoadBalancer creates a load balancer (LB) with an outbound rule for guest cluster egress; azure cloud provider will reuse this LB to add a public ip address and the load balancer rules
func createLoadBalancer(ctx context.Context, subscriptionID string, resourceGroupName string, infraID string, location string, publicIPAddress *armnetwork.PublicIPAddress, azureCreds azcore.TokenCredential) error {
	idPrefix := fmt.Sprintf("subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", subscriptionID, resourceGroupName)
	loadBalancerName := infraID

	loadBalancerClient, err := armnetwork.NewLoadBalancersClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create load balancer client, %w", err)
	}

	pollerResp, err := loadBalancerClient.BeginCreateOrUpdate(ctx,
		resourceGroupName,
		loadBalancerName,
		armnetwork.LoadBalancer{
			Location: ptr.To(location),
			SKU: &armnetwork.LoadBalancerSKU{
				Name: ptr.To(armnetwork.LoadBalancerSKUNameStandard),
			},
			Properties: &armnetwork.LoadBalancerPropertiesFormat{
				FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
					{
						Name: &infraID,
						Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
							PrivateIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodDynamic),
							PublicIPAddress:           publicIPAddress,
						},
					},
				},
				BackendAddressPools: []*armnetwork.BackendAddressPool{
					{
						Name: &infraID,
					},
				},
				Probes: []*armnetwork.Probe{
					{
						Name: &infraID,
						Properties: &armnetwork.ProbePropertiesFormat{
							Protocol:          ptr.To(armnetwork.ProbeProtocolHTTP),
							Port:              ptr.To[int32](30595),
							IntervalInSeconds: ptr.To[int32](5),
							ProbeThreshold:    ptr.To[int32](2),
							RequestPath:       ptr.To("/healthz"),
						},
					},
				},
				// This outbound rule follows the guidance found here
				// https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-outbound-connections#outboundrules
				OutboundRules: []*armnetwork.OutboundRule{
					{
						Name: ptr.To(infraID),
						Properties: &armnetwork.OutboundRulePropertiesFormat{
							BackendAddressPool: &armnetwork.SubResource{
								ID: ptr.To(fmt.Sprintf("/%s/%s/backendAddressPools/%s", idPrefix, loadBalancerName, infraID)),
							},
							FrontendIPConfigurations: []*armnetwork.SubResource{
								{
									ID: ptr.To(fmt.Sprintf("/%s/%s/frontendIPConfigurations/%s", idPrefix, loadBalancerName, infraID)),
								},
							},
							Protocol:               ptr.To(armnetwork.LoadBalancerOutboundRuleProtocolAll),
							AllocatedOutboundPorts: ptr.To[int32](1024),
							EnableTCPReset:         ptr.To(true),
							IdleTimeoutInMinutes:   ptr.To[int32](4),
						},
					},
				},
			},
		}, nil)

	if err != nil {
		return fmt.Errorf("failed to create guest cluster egress load balancer: %w", err)
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting to create guest cluster egress load balancer: %w", err)
	}
	return nil
}

// assignRole assigns a scoped role to the service principal assignee
func assignRole(ctx context.Context, subscriptionID, infraID, component, assigneeID, role, scope string, azureCreds *azidentity.DefaultAzureCredential) error {
	roleAssignmentClient, err := azureauth.NewRoleAssignmentsClient(subscriptionID, azureCreds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new role assignments client: %w", err)
	}

	// Generate the role assignment name
	roleAssignmentName := util.GenerateRoleAssignmentName(infraID, component, scope)

	// Generate the role definition ID
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, role)

	// Generate the role assignment properties
	roleAssignmentProperties := azureauth.RoleAssignmentCreateParameters{
		Properties: &azureauth.RoleAssignmentProperties{
			PrincipalID:      ptr.To(assigneeID),
			RoleDefinitionID: ptr.To(roleDefinitionID),
			Scope:            ptr.To(scope),
		},
	}

	// Create the role assignment
	_, err = roleAssignmentClient.Create(ctx, scope, roleAssignmentName, roleAssignmentProperties, nil)
	if err != nil {
		// Azure will return an error if the role assignment already exists, but we can ignore it since the role
		// assignment already exists.
		if !strings.Contains(err.Error(), "The role assignment already exists") {
			return fmt.Errorf("failed to create role assignment: %w", err)
		}
		log.Log.Info("WARNING: Role assignment already exists.", "role", role, "assigneeID", assigneeID, "scope", scope)
	}
	log.Log.Info("successfully created role assignment", "role", role, "assigneeID", assigneeID, "scope", scope)
	return nil
}

func getAzureToken(azureCreds *azidentity.DefaultAzureCredential) (azcore.AccessToken, error) {
	token, err := azureCreds.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("failed to get access token: %w", err)
	}

	return token, nil
}

func getObjectIDFromClientID(clientID string, token azcore.AccessToken) (string, error) {
	filterQuery := "$filter=appId eq '" + clientID + "'"
	url := graphAPIEndpoint + "?" + strings.ReplaceAll(filterQuery, " ", "%20")

	// Make the API request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set Authorization header
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	// Parse response
	var result ServicePrincipalResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Value) == 0 {
		return "", fmt.Errorf("no object id found for client id: %s", clientID)
	}

	if len(result.Value) > 1 {
		return "", fmt.Errorf("more than one object id found for client id: %s", clientID)
	}

	return result.Value[0].ID, nil
}
