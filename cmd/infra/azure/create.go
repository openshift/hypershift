package azure

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

const (
	VirtualNetworkAddressPrefix       = "10.0.0.0/16"
	VirtualNetworkLinkLocation        = "global"
	VirtualNetworkSubnetAddressPrefix = "10.0.0.0/24"
)

// NewCreateCommand creates a new cobra command for creating Azure infrastructure resources for a HostedCluster
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates Azure infrastructure resources for a HostedCluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		Location: "eastus",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID(required)")
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to a credentials file (required). This file is used to create credentials used to create the necessary Azure resources for the HostedCluster.")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Azure location where HostedCluster infrastructure should be created")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the HostedCluster")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the HostedCluster")
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under. If not provided, a new resource group will be created.")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, "The Network Security Group ID to use in the default NodePool. If not provided, a new Network Security Group will be created.")
	cmd.Flags().StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The subnet ID where the VMs will be placed. If not provided, a new subnet will be created.")
	cmd.Flags().StringVar(&opts.VnetID, "vnet-id", opts.VnetID, "The VNet ID to use. If not provided, a new VNet will be created.")
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

// Run is the main function responsible for creating the Azure infrastructure resources for a HostedCluster.
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

	// Initialize managers
	rgMgr := NewResourceGroupManager(subscriptionID, azureCreds)
	netMgr := NewNetworkManager(subscriptionID, azureCreds)
	imgMgr := NewImageManager(subscriptionID, azureCreds)
	rbacMgr := NewRBACManager(subscriptionID, azureCreds)

	// Create main resource group
	resourceGroupName, msg, err := rgMgr.CreateOrGetResourceGroup(ctx, o, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create a resource group: %w", err)
	}
	result.ResourceGroupName = resourceGroupName
	l.Info(msg, "name", resourceGroupName)

	// Get base DNS zone ID
	result.PublicZoneID, err = netMgr.GetBaseDomainID(ctx, o.BaseDomain)
	if err != nil {
		return nil, err
	}

	// Handle network security group
	nsgResourceGroupName := ""
	if len(o.NetworkSecurityGroupID) > 0 {
		result.SecurityGroupID = o.NetworkSecurityGroupID
		_, nsgResourceGroupName, err = azureutil.GetNameAndResourceGroupFromNetworkSecurityGroupID(o.NetworkSecurityGroupID)
		if err != nil {
			return nil, err
		}
		l.Info("Using existing network security group", "ID", result.SecurityGroupID)
	} else {
		nsgResourceGroupName = o.Name + "-nsg"
		nsgResourceGroupName, msg, err = rgMgr.CreateOrGetResourceGroup(ctx, o, nsgResourceGroupName)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource group for network security group: %w", err)
		}
		l.Info(msg, "name", nsgResourceGroupName)

		nsgID, err := netMgr.CreateSecurityGroup(ctx, nsgResourceGroupName, o.Name, o.InfraID, o.Location)
		if err != nil {
			return nil, err
		}
		result.SecurityGroupID = nsgID
		l.Info("Successfully created network security group", "ID", result.SecurityGroupID)
	}

	// Handle subnet
	if len(o.SubnetID) > 0 {
		result.SubnetID = o.SubnetID
		l.Info("Using existing subnet", "ID", result.SubnetID)
	}

	// Handle virtual network
	vnetResourceGroupName := ""
	if len(o.VnetID) > 0 {
		result.VNetID = o.VnetID
		_, vnetResourceGroupName, err = azureutil.GetVnetNameAndResourceGroupFromVnetID(o.VnetID)
		if err != nil {
			return nil, err
		}
		l.Info("Using existing vnet", "ID", result.VNetID)
	} else {
		vnetResourceGroupName = o.Name + "-vnet"
		vnetResourceGroupName, msg, err = rgMgr.CreateOrGetResourceGroup(ctx, o, vnetResourceGroupName)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource group for virtual network: %w", err)
		}
		l.Info(msg, "name", vnetResourceGroupName)

		vnet, err := netMgr.CreateVirtualNetwork(ctx, vnetResourceGroupName, o.Name, o.InfraID, o.Location, o.SubnetID, result.SecurityGroupID)
		if err != nil {
			return nil, err
		}
		result.SubnetID = *vnet.Properties.Subnets[0].ID
		result.VNetID = *vnet.ID
		l.Info("Successfully created vnet", "ID", result.VNetID)
	}

	// Handle managed identities and RBAC
	if o.ManagedIdentitiesFile != "" {
		result.ControlPlaneMIs = &hyperv1.AzureResourceManagedIdentities{}
		managedIdentitiesRaw, err := os.ReadFile(o.ManagedIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --managed-identities-file %s: %w", o.ManagedIdentitiesFile, err)
		}
		if err := yaml.Unmarshal(managedIdentitiesRaw, &result.ControlPlaneMIs.ControlPlane); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --managed-identities-file: %w", err)
		}

		if o.AssignServicePrincipalRoles {
			if err := rbacMgr.AssignControlPlaneRoles(ctx, o, result.ControlPlaneMIs, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName); err != nil {
				return nil, err
			}
		}
	}

	// Handle data plane identities
	if o.DataPlaneIdentitiesFile != "" {
		dataPlaneIdentitiesRaw, err := os.ReadFile(o.DataPlaneIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --data-plane-identities-file %s: %w", o.DataPlaneIdentitiesFile, err)
		}
		if err := yaml.Unmarshal(dataPlaneIdentitiesRaw, &result.DataPlaneIdentities); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --data-plane-identities-file: %w", err)
		}

		if o.AssignServicePrincipalRoles {
			if err := rbacMgr.AssignDataPlaneRoles(ctx, o, result.DataPlaneIdentities, resourceGroupName); err != nil {
				return nil, err
			}
		}
	}

	// Create DNS infrastructure
	privateDNSZoneID, privateDNSZoneName, err := netMgr.CreatePrivateDNSZone(ctx, resourceGroupName, o.Name, o.BaseDomain)
	if err != nil {
		return nil, err
	}
	result.PrivateZoneID = privateDNSZoneID
	l.Info("Successfully created private DNS zone", "name", privateDNSZoneName)

	err = netMgr.CreatePrivateDNSZoneLink(ctx, resourceGroupName, o.Name, o.InfraID, result.VNetID, privateDNSZoneName)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created private DNS zone link")

	// Create load balancer infrastructure
	publicIPAddress, err := netMgr.CreatePublicIPAddressForLB(ctx, resourceGroupName, o.InfraID, o.Location)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created public IP address for guest cluster egress load balancer")

	err = netMgr.CreateLoadBalancer(ctx, resourceGroupName, o.InfraID, o.Location, publicIPAddress)
	if err != nil {
		return nil, err
	}
	l.Info("Successfully created guest cluster egress load balancer")

	// Create RHCOS image if needed
	if o.RHCOSImage != "" {
		result.BootImageID, err = imgMgr.CreateRHCOSImages(ctx, l, o, resourceGroupName)
		if err != nil {
			return nil, fmt.Errorf("failed to create RHCOS image: %w", err)
		}
	}

	// Serialize the result to the output file if it was provided
	if o.OutputFile != "" {
		resultSerialized, err := yaml.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize result: %w", err)
		}
		if err := os.WriteFile(o.OutputFile, resultSerialized, 0644); err != nil {
			l.Error(err, "Writing output file failed", "Output File", o.OutputFile, "data", string(resultSerialized))
			return nil, fmt.Errorf("failed to write result to --output-file: %w", err)
		}
	}

	return &result, nil
}
