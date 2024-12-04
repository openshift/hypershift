package azure

import (
	"context"
	"errors"
	"fmt"
	"github.com/openshift/hypershift/support/azureutil"
	"os"
	"os/exec"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/pageblob"
)

const (
	VirtualNetworkAddressPrefix       = "10.0.0.0/16"
	VirtualNetworkLinkLocation        = "global"
	VirtualNetworkSubnetAddressPrefix = "10.0.0.0/24"

	azureDisk     = "azure-disk"
	azureFile     = "azure-file"
	ciro          = "ciro"
	cloudProvider = "cloud-provider"
	cncc          = "cncc"
	cpo           = "cpo"
	ingress       = "ingress"
	nodePoolMgmt  = "capz"
)

type CreateInfraOptions struct {
	Name                            string
	BaseDomain                      string
	Location                        string
	InfraID                         string
	CredentialsFile                 string
	Credentials                     *util.AzureCreds
	OutputFile                      string
	RHCOSImage                      string
	ResourceGroupName               string
	VnetID                          string
	NetworkSecurityGroupID          string
	ResourceGroupTags               map[string]string
	SubnetID                        string
	ManagedIdentityKeyVaultName     string
	ManagedIdentityKeyVaultTenantID string
	TechPreviewEnabled              bool
	ManagedIdentitiesFile           string
	AssignServicePrincipalRoles     bool
	DNSZoneRG                       string
}

type CreateInfraOutput struct {
	BaseDomain        string                                 `json:"baseDomain"`
	PublicZoneID      string                                 `json:"publicZoneID"`
	PrivateZoneID     string                                 `json:"privateZoneID"`
	Location          string                                 `json:"region"`
	ResourceGroupName string                                 `json:"resourceGroupName"`
	VNetID            string                                 `json:"vnetID"`
	SubnetID          string                                 `json:"subnetID"`
	BootImageID       string                                 `json:"bootImageID"`
	InfraID           string                                 `json:"infraID"`
	MachineIdentityID string                                 `json:"machineIdentityID"`
	SecurityGroupID   string                                 `json:"securityGroupID"`
	ControlPlaneMIs   hyperv1.AzureResourceManagedIdentities `json:"controlPlaneMIs"`
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
		managedIdentitiesRaw, err := os.ReadFile(o.ManagedIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --managed-identities-file %s: %w", o.ManagedIdentitiesFile, err)
		}
		if err := yaml.Unmarshal(managedIdentitiesRaw, &result.ControlPlaneMIs.ControlPlane); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --managed-identities-file: %w", err)
		}

		components := map[string]string{
			cpo:           result.ControlPlaneMIs.ControlPlane.ControlPlaneOperator.ClientID,
			ciro:          result.ControlPlaneMIs.ControlPlane.ImageRegistry.ClientID,
			nodePoolMgmt:  result.ControlPlaneMIs.ControlPlane.NodePoolManagement.ClientID,
			cloudProvider: result.ControlPlaneMIs.ControlPlane.CloudProvider.ClientID,
			azureFile:     result.ControlPlaneMIs.ControlPlane.File.ClientID,
			azureDisk:     result.ControlPlaneMIs.ControlPlane.Disk.ClientID,
			ingress:       result.ControlPlaneMIs.ControlPlane.Ingress.ClientID,
			cncc:          result.ControlPlaneMIs.ControlPlane.Network.ClientID,
		}

		if o.AssignServicePrincipalRoles {
			for component, clientID := range components {
				objectID, err := findObjectId(clientID)
				if err != nil {
					return nil, err
				}
				err = assignContributorRole(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, o.DNSZoneRG, component, objectID)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if o.TechPreviewEnabled && o.ManagedIdentitiesFile == "" {
		if o.ManagedIdentityKeyVaultName != "" {
			result.ControlPlaneMIs.ControlPlane.ManagedIdentitiesKeyVault.Name = o.ManagedIdentityKeyVaultName

		}
		if o.ManagedIdentityKeyVaultTenantID != "" {
			result.ControlPlaneMIs.ControlPlane.ManagedIdentitiesKeyVault.TenantID = o.ManagedIdentityKeyVaultTenantID
		}

		// Create ServicePrincipals with backing certificates
		cmdStr := buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, ingress, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err := createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.Ingress.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.Ingress.CertificateName = fmt.Sprintf("%s-%s", ingress, o.InfraID)
		l.Info("Successfully created ingress service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, cncc, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.Network.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.Network.CertificateName = fmt.Sprintf("%s-%s", cncc, o.InfraID)
		l.Info("Successfully created cncc service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, azureDisk, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.Disk.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.Disk.CertificateName = fmt.Sprintf("%s-%s", azureDisk, o.InfraID)
		l.Info("Successfully created azure-disk service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, azureFile, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.File.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.File.CertificateName = fmt.Sprintf("%s-%s", azureFile, o.InfraID)
		l.Info("Successfully created azure-file service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, cloudProvider, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.CloudProvider.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.CloudProvider.CertificateName = fmt.Sprintf("%s-%s", cloudProvider, o.InfraID)
		l.Info("Successfully created cloud provider service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, nodePoolMgmt, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.NodePoolManagement.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.NodePoolManagement.CertificateName = fmt.Sprintf("%s-%s", nodePoolMgmt, o.InfraID)
		l.Info("Successfully created nodepool management service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, cpo, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.ControlPlaneOperator.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.ControlPlaneOperator.CertificateName = fmt.Sprintf("%s-%s", cpo, o.InfraID)
		l.Info("Successfully created cpo service principal", "ID", clientID)

		cmdStr = buildCreateServicePrincipalCommand(subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, ciro, o.InfraID, o.ManagedIdentityKeyVaultName, o.DNSZoneRG)
		clientID, err = createServicePrincipalWithCertificate(cmdStr)
		if err != nil {
			return nil, err
		}
		result.ControlPlaneMIs.ControlPlane.ImageRegistry.ClientID = clientID
		result.ControlPlaneMIs.ControlPlane.ImageRegistry.CertificateName = fmt.Sprintf("%s-%s", ciro, o.InfraID)
		l.Info("Successfully created ciro service principal", "ID", clientID)
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

// createServicePrincipalWithCertificate runs the command to create a Service Principal with a role(s) over resource group(s),
// create a new certificate for it, and store it in an existing key vault. The client ID of the service principal will be returned.
func createServicePrincipalWithCertificate(cmdStr string) (string, error) {
	// Run the az cli command and capture the output, which should be just the client ID with a newline character
	cmd := exec.Command("sh", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create service principal in Azure AD cluster: %w", err)
	}

	if strings.Contains(string(output), "ERROR") {
		return "", errors.New(string(output))
	}

	//Trim off any newline characters from the output
	clientID := strings.ReplaceAll(string(output), "\n", "")

	return clientID, nil
}

// buildCreateServicePrincipalCommand builds the command string to create a service principal, output the results in a JSON format to get the client ID, and strips off the quotes.
func buildCreateServicePrincipalCommand(subscriptionID, managedResourceGroupName, nsgResourceGroupName, vnetResourceGroupName, component, infraID, managedIdentityKeyVaultName, dnsZoneRGName string) string {
	// Create a name with the component and infraID; this is used for both the service principal name and its certificate name.
	name := fmt.Sprintf("%s-%s", component, infraID)

	// By default, give each service principal contributor access over the managed resource group. Some components require additional permissions over other resource groups.
	// TODO HOSTEDCP-1520 - these permissions will likely change once we complete this story.
	managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroupName)
	nsgRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, nsgResourceGroupName)
	vnetRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, vnetResourceGroupName)
	dnsZoneRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, dnsZoneRGName)
	scopes := managedRG
	switch component {
	case cloudProvider:
		scopes = fmt.Sprintf("%s %s", scopes, nsgRG)
	case ingress:
		scopes = fmt.Sprintf("%s %s %s", scopes, vnetRG, dnsZoneRG)
	}

	// The command creates a Service Principal with a role(s) over resource group(s), create a new certificate for it, and store it in an existing keyvault
	// '--only-show-errors' this flag only shows errors and not warning messages which can be verbose and mess up parsing out the client ID
	// "| jq '.appId' | sed 's/"//g'" this just reads the appId which is the client ID and the sed strips off the json quotes around the value
	cmdStr := fmt.Sprintf("az ad sp create-for-rbac --name %s --role \"Contributor\" --scopes %s --create-cert --cert %s --keyvault %s --output json --only-show-errors  | jq '.appId' | sed 's/\"//g'", name, scopes, name, managedIdentityKeyVaultName)
	return cmdStr
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

	if vnet.Properties.Subnets == nil || len(vnet.Properties.Subnets) < 1 {
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

// AssignContributorRole assigns the contributor role to the service principal asigneeID in the managed resource group
// and the network or vnet resource groups based on the component
func assignContributorRole(subscriptionID, managedResourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneResourceGroupName, component, asigneeID string) error {
	managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroupName)
	nsgRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, nsgResourceGroupName)
	vnetRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, vnetResourceGroupName)
	dnsZoneRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, dnsZoneResourceGroupName)

	scopes := []string{managedRG}

	switch component {
	case cloudProvider:
		scopes = append(scopes, nsgRG)
	case ingress:
		scopes = append(scopes, vnetRG, dnsZoneRG)
	}

	for _, scope := range scopes {
		cmdStr := fmt.Sprintf("az role assignment create --assignee-object-id %s --role \"Contributor\" --scope %s --assignee-principal-type \"ServicePrincipal\" ", asigneeID, scope)
		_, err := execAzCommand(cmdStr)
		if err != nil {
			return fmt.Errorf("failed to assign contributor role to service principal, %s for scope %s : %w", asigneeID, scope, err)
		}
		log.Log.Info("Successfully assigned contributor role to service principal", "ID", asigneeID, "scope", scope)
	}

	return nil
}

func findObjectId(appID string) (string, error) {
	cmdStr := fmt.Sprintf("az ad sp show --id %s --query id | sed 's/\"//g'", appID)

	output, err := execAzCommand(cmdStr)
	if err != nil {
		return "", fmt.Errorf("failed to find object ID for service principal, %s : %w", appID, err)

	}
	objectID := strings.ReplaceAll(string(output), "\n", "")

	return objectID, nil
}

func execAzCommand(cmdStr string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute az command: %w", err)
	}

	if strings.Contains(string(output), "ERROR") {
		return nil, errors.New(string(output))
	}

	return output, nil
}
