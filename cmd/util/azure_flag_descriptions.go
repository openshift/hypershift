package util

// Azure flag descriptions for CLI commands.
// These constants are shared between cluster and nodepool commands to ensure consistency.

const (
	// Credentials
	AzureCredsDescription = "Path to an Azure credentials file (JSON format) containing subscription ID, tenant ID, client ID, and client secret. These credentials are used to create and manage Azure resources for the HostedCluster. (required)"

	// ARO HCP (managed Azure) identity flags
	KMSCredentialsSecretNameDescription    = "The name of a secret, in Azure KeyVault, containing the JSON UserAssignedIdentityCredentials used in KMS to authenticate to Azure."
	ManagedIdentitiesFileDescription       = "Path to a file containing the managed identities configuration in JSON format."
	DataPlaneIdentitiesFileDescription     = "Path to a file containing the client IDs of the managed identities for the data plane configured in JSON format."
	AssignCustomHCPRolesDescription        = "Assign custom roles to HCP identities."
	AssignServicePrincipalRolesDescription = "Assign required Azure RBAC roles to identities (managed identities for ARO HCP, workload identities for self-managed)."

	// Location and availability
	LocationDescription          = "Azure region where the cluster and its resources will be created (e.g. eastus, westus2, northeurope)."
	AvailabilityZonesDescription = "Availability zones for NodePool placement (e.g. 1,2,3). One NodePool will be created per zone. Omit if the region does not support availability zones."
	AvailabilityZoneDescription  = "Availability zone for the NodePool (e.g. 1, 2, or 3). Omit if the region does not support availability zones."

	// Resource group
	ResourceGroupNameDescription = "Name of an existing resource group where HostedCluster infrastructure resources will be created. If omitted, a new resource group will be created."
	ResourceGroupTagsDescription = "Additional tags to apply to the resource group (e.g. 'environment=prod,team=platform')."
	DNSZoneRGNameDescription     = "Name of the resource group containing your Azure DNS zone. Required for the ingress controller to create DNS records."

	// Networking
	VnetIDDescription                 = "Full resource ID of an existing VNET to use for the cluster (e.g. /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/virtualNetworks/<name>). If omitted, a new VNET will be created."
	SubnetIDDescription               = "Full resource ID of an existing subnet where VMs will be placed. If omitted for cluster creation, a new subnet will be created."
	NetworkSecurityGroupIDDescription = "Full resource ID of an existing Network Security Group for the default NodePool. If omitted, a new NSG will be created."

	// Identity
	WorkloadIdentitiesFileDescription = "Path to a JSON file containing workload identity client IDs that map Azure identities to HyperShift components. Required for self-managed Azure clusters using workload identity authentication."
	OIDCIssuerURLDescription          = "URL of the OIDC identity provider used for workload identity federation. This enables Azure workload identities to authenticate with the cluster."
	SATokenIssuerKeyPathDescription   = "Path to the RSA private key file used to sign service account tokens. Required for OIDC-based workload identity authentication."
	AutoAssignRolesDescription        = "Automatically assign required Azure RBAC roles to workload identities. This grants the identities permissions to manage Azure resources (DNS, networking, storage) for the cluster."

	// Encryption
	EncryptionKeyIDDescription     = "Azure Key Vault key identifier used to encrypt etcd data via KMSv2 (format: https://<vault>.vault.azure.net/keys/<key>/<version>)."
	EncryptionAtHostDescription    = "Enable host-based encryption for VM disks and temp disks. Valid values: Enabled, Disabled."
	DiskEncryptionSetIDDescription = "Full resource ID of an Azure Disk Encryption Set used to encrypt NodePool OS disks with customer-managed keys."

	// VM configuration
	InstanceTypeDescription = "Azure VM size for NodePool instances (e.g. Standard_D4s_v4, Standard_D8s_v5)."
	RootDiskSizeDescription = "Size of the OS disk in GB for each NodePool VM. Minimum: 16 GB."

	// Disk configuration
	DiskStorageAccountTypeDescription = "Azure storage type for NodePool OS disks. Valid values: Premium_LRS, StandardSSD_LRS, Standard_LRS."
	EnableEphemeralOSDiskDescription  = "Use ephemeral OS disks for faster VM provisioning and lower latency. Note: Data is lost when VMs are deallocated."

	// Image configuration
	ImageGenerationDescription      = "Hyper-V generation for VM images. Valid values: Gen1, Gen2. Gen2 is recommended for most modern workloads."
	MarketplacePublisherDescription = "Publisher name for Azure Marketplace image (e.g. redhat). Only needed if overriding the default RHCOS image."
	MarketplaceOfferDescription     = "Offer name for Azure Marketplace image (e.g. rhcos). Only needed if overriding the default RHCOS image."
	MarketplaceSKUDescription       = "SKU for Azure Marketplace image (e.g. rhcos-414). Only needed if overriding the default RHCOS image."
	MarketplaceVersionDescription   = "Version of the Azure Marketplace image (e.g. 414.92.2024020901 or 'latest'). Only needed if overriding the default RHCOS image."

	// Diagnostics
	DiagnosticsStorageAccountTypeDescription = "Boot diagnostics storage type for troubleshooting VM issues. Valid values: Disabled, Managed (Azure-managed storage), UserManaged (your storage account)."
	DiagnosticsStorageAccountURIDescription  = "URI of your storage account for boot diagnostics logs. Required when using UserManaged diagnostics type."

	// Destroy options
	PreserveResourceGroupDescription = "Keep the resource group after cluster deletion. Only cluster-specific resources within the group will be removed."

	// Destroy-specific location and resource group descriptions
	LocationDestroyDescription          = "Azure region of the cluster. Inferred from the HostedCluster if it exists; only required if the cluster resource has already been deleted."
	AzureCredsDestroyDescription        = "Path to an Azure credentials file (JSON format) used to authenticate and delete Azure resources."
	ResourceGroupNameDestroyDescription = "Name of the resource group containing the cluster resources to delete. Inferred from the HostedCluster if it exists; only required if the cluster resource has already been deleted."

	// Infrastructure command specific flags
	AssignIdentityRolesDescription          = "Automatically assign required Azure RBAC roles to workload identities. This grants the identities permissions to manage Azure resources."
	DisableClusterCapabilitiesDescription   = "Comma-separated list of cluster capabilities to disable (e.g. ImageRegistry). Disabled capabilities will not have corresponding workload identities created."
	InfraIDDescription                      = "Unique identifier used to name and tag Azure resources. This ID will be incorporated into resource names and Azure tags."
	BaseDomainInfraDescription              = "Base DNS domain for the cluster (e.g. example.com). A public DNS zone for this domain must exist in your Azure subscription."
	InfraOutputFileDescription              = "Path to file where the infrastructure output will be saved in YAML format. Contains resource IDs and other information needed for cluster creation."
	WorkloadIdentitiesOutputFileDescription = "Path where generated workload identities JSON will be saved. This output file can be passed to 'hypershift create infra azure' using --workload-identities-file."

	// Common flags
	NameDescription  = "A name for the HostedCluster. This name is used to identify resources and must be unique within the namespace."
	CloudDescription = "Azure cloud environment. Valid values: AzurePublicCloud, AzureUSGovernmentCloud, AzureChinaCloud."
)
