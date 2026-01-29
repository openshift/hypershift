package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"

	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		Cloud:    "AzurePublicCloud",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID(required)")
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to a credentials file (required). This file is used to create credentials used to create the necessary Azure resources for the HostedCluster.")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Azure location where HostedCluster infrastructure should be created")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "Azure cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud)")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the HostedCluster")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the HostedCluster")
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under. If not provided, a new resource group will be created.")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, "The Network Security Group ID to use in the default NodePool. If not provided, a new Network Security Group will be created.")
	cmd.Flags().StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The subnet ID where the VMs will be placed. If not provided, a new subnet will be created.")
	cmd.Flags().StringVar(&opts.VnetID, "vnet-id", opts.VnetID, "The VNet ID to use. If not provided, a new VNet will be created.")
	cmd.Flags().StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, "Additional tags to apply to the resource group created (e.g. 'key1=value1,key2=value2')")
	cmd.Flags().StringVar(&opts.ManagedIdentitiesFile, "managed-identities-file", opts.ManagedIdentitiesFile, "Path to file containing ARO HCP managed identities JSON")
	cmd.Flags().StringVar(&opts.DataPlaneIdentitiesFile, "data-plane-identities-file", opts.DataPlaneIdentitiesFile, "Path to file containing ARO HCP data plane identities JSON")
	cmd.Flags().StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, "Path to file containing self-managed Azure workload identities JSON")

	// RBAC and identity role assignment flags
	cmd.Flags().BoolVar(&opts.AssignServicePrincipalRoles, "assign-identity-roles", opts.AssignServicePrincipalRoles, util.AssignIdentityRolesDescription)
	cmd.Flags().StringVar(&opts.DNSZoneRG, "dns-zone-rg-name", opts.DNSZoneRG, util.DNSZoneRGNameDescription)
	cmd.Flags().BoolVar(&opts.AssignCustomHCPRoles, "assign-custom-hcp-roles", opts.AssignCustomHCPRoles, util.AssignCustomHCPRolesDescription)
	cmd.Flags().StringSliceVar(&opts.DisableClusterCapabilities, "disable-cluster-capabilities", opts.DisableClusterCapabilities, util.DisableClusterCapabilitiesDescription)

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("name")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if _, err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create infrastructure")
			return err
		}
		l.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}

// DefaultOptions returns CreateInfraOptions with default values for self-managed Azure
func DefaultOptions() *CreateInfraOptions {
	return &CreateInfraOptions{
		Location: "eastus",
		Cloud:    "AzurePublicCloud",
	}
}

// BindProductFlags binds flags for the product CLI (hcp) infra create azure command.
// This exposes only the self-managed Azure flags relevant for the productized CLI.
func BindProductFlags(opts *CreateInfraOptions, flags *pflag.FlagSet) {
	// Required flags
	flags.StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)
	flags.StringVar(&opts.Name, "name", opts.Name, "A name for the HostedCluster")

	// Location and cloud
	flags.StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	flags.StringVar(&opts.Cloud, "cloud", opts.Cloud, "Azure cloud environment (AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud)")
	flags.StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, util.BaseDomainInfraDescription)

	// Resource group and tags
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	flags.StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, util.ResourceGroupTagsDescription)

	// Networking
	flags.StringVar(&opts.VnetID, "vnet-id", opts.VnetID, util.VnetIDDescription)
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, util.SubnetIDDescription)
	flags.StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, util.NetworkSecurityGroupIDDescription)

	// Self-managed Azure identity flags
	flags.StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, util.WorkloadIdentitiesFileDescription)

	// RBAC and role assignment
	flags.BoolVar(&opts.AssignServicePrincipalRoles, "assign-identity-roles", opts.AssignServicePrincipalRoles, util.AssignIdentityRolesDescription)
	flags.StringVar(&opts.DNSZoneRG, "dns-zone-rg-name", opts.DNSZoneRG, util.DNSZoneRGNameDescription)
	flags.BoolVar(&opts.AssignCustomHCPRoles, "assign-custom-hcp-roles", opts.AssignCustomHCPRoles, util.AssignCustomHCPRolesDescription)
	flags.StringSliceVar(&opts.DisableClusterCapabilities, "disable-cluster-capabilities", opts.DisableClusterCapabilities, util.DisableClusterCapabilitiesDescription)

	// Output
	flags.StringVar(&opts.OutputFile, "output-file", opts.OutputFile, util.InfraOutputFileDescription)
}

// Run is the main function responsible for creating the Azure infrastructure resources for a HostedCluster.
func (o *CreateInfraOptions) Run(ctx context.Context, l logr.Logger) (*CreateInfraOutput, error) {
	// Validate deployment model flags to prevent conflicts between ARO HCP and self-managed Azure
	if err := o.validateDeploymentModelFlags(); err != nil {
		return nil, err
	}

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
	rgMgr := NewResourceGroupManager(subscriptionID, azureCreds, o.Cloud)
	netMgr := NewNetworkManager(subscriptionID, azureCreds, o.Cloud)
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

	// Handle workload identities
	if o.WorkloadIdentitiesFile != "" {
		workloadIdentitiesRaw, err := os.ReadFile(o.WorkloadIdentitiesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read --workload-identities-file %s: %w", o.WorkloadIdentitiesFile, err)
		}
		if err := json.Unmarshal(workloadIdentitiesRaw, &result.WorkloadIdentities); err != nil {
			return nil, fmt.Errorf("failed to unmarshal --workload-identities-file: %w", err)
		}

		if o.AssignServicePrincipalRoles {
			if err := rbacMgr.AssignWorkloadIdentities(ctx, o, result.WorkloadIdentities, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName); err != nil {
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

// Validate validates the CreateInfraOptions before running the command
func (o *CreateInfraOptions) Validate() error {
	// base-domain is required for DNS operations
	if o.BaseDomain == "" {
		return fmt.Errorf("--base-domain is required")
	}
	return nil
}

// validateDeploymentModelFlags validates that deployment model flags are not conflicting
// This ensures separation between ARO HCP and self-managed Azure deployment models
func (o *CreateInfraOptions) validateDeploymentModelFlags() error {
	// Check for conflicts between ARO HCP and self-managed Azure flags
	if o.ManagedIdentitiesFile != "" && o.WorkloadIdentitiesFile != "" {
		return fmt.Errorf("flags --managed-identities-file and --workload-identities-file are mutually exclusive")
	}
	if o.DataPlaneIdentitiesFile != "" && o.WorkloadIdentitiesFile != "" {
		return fmt.Errorf("flags --data-plane-identities-file and --workload-identities-file are mutually exclusive")
	}

	// Ensure at least one identity configuration is provided
	hasAnyIdentityConfig := o.ManagedIdentitiesFile != "" ||
		o.DataPlaneIdentitiesFile != "" ||
		o.WorkloadIdentitiesFile != ""

	if !hasAnyIdentityConfig {
		return fmt.Errorf("at least one identity configuration must be provided:\n" +
			"  - For ARO HCP: use --managed-identities-file and --data-plane-identities-file\n" +
			"  - For self-managed Azure: use --workload-identities-file (created with 'hypershift create iam azure')")
	}

	return nil
}
