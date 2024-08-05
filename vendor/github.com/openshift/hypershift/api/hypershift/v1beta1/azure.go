package v1beta1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

type AzureVMImageType string

const (
	ImageID          AzureVMImageType = "ImageID"
	AzureMarketplace AzureVMImageType = "AzureMarketplace"
)

// AzureVMImage represents the different types of image data that can be provided for an Azure VM.
// +union
type AzureVMImage struct {
	// Type is the type of image data that will be provided to the Azure VM. This can be either "ImageID" or
	// "AzureMarketplace".
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=ImageID;AzureMarketplace
	// +unionDiscriminator
	Type AzureVMImageType `json:"azureImageType"`

	// ImageID is the Azure resource ID of a VHD image to use to boot the Azure VMs from.
	//
	// +optional
	// +unionMember
	ImageID *string `json:"imageID,omitempty"`

	// AzureMarketplace contains the Azure Marketplace image info to use to boot the Azure VMs from.
	//
	// +optional
	// +unionMember
	AzureMarketplace *MarketplaceImage `json:"azureMarketplace,omitempty"`
}

type AzureNodePoolPlatform struct {
	// VMSize is the Azure VM instance type to use for the nodes being created in the nodepool.
	//
	// +kubebuilder:validation:Required
	VMSize string `json:"vmsize"`

	// ImageID is the id of the image to boot from. If unset, the default image at the location below will be used and
	// is expected to exist: subscription/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Compute/images/rhcos.x86_64.vhd.
	// The <subscriptionID> and the <resourceGroupName> are expected to be the same resource group documented in the
	// Hosted Cluster specification respectively, HostedCluster.Spec.Platform.Azure.SubscriptionID and
	// HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// +kubebuilder:validation:Required
	Image AzureVMImage `json:"image"`

	// DiskSizeGB is the size in GB to assign to the OS disk
	// CAPZ default is 30GB, https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/b3708019a67ff19407b87d63c402af94ca4246f6/api/v1beta1/types.go#L599
	//
	// +kubebuilder:default:=30
	// +kubebuilder:validation:Minimum=16
	// +optional
	DiskSizeGB int32 `json:"diskSizeGB,omitempty"`

	// DiskStorageAccountType is the disk storage account type to use. Valid values are:
	// * Standard_LRS: HDD
	// * StandardSSD_LRS: Standard SSD
	// * Premium_LRS: Premium SDD
	// * UltraSSD_LRS: Ultra SDD
	//
	// Defaults to Premium_LRS. For more details, visit the Azure documentation:
	// https://docs.microsoft.com/en-us/azure/virtual-machines/disks-types#disk-type-comparison
	//
	// +kubebuilder:default:=Premium_LRS
	// +kubebuilder:validation:Enum=Standard_LRS;StandardSSD_LRS;Premium_LRS;UltraSSD_LRS
	// +optional
	DiskStorageAccountType string `json:"diskStorageAccountType,omitempty"`

	// AvailabilityZone is the failure domain identifier where the VM should be attached to. This must not be specified
	// for clusters in a location that does not support AvailabilityZone.
	//
	// +optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// encryptionAtHost enables encryption at host on virtual machines. According to Microsoft documentation, this
	// means data stored on the VM host is encrypted at rest and flows encrypted to the Storage service. See
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disks-enable-host-based-encryption-portal?tabs=azure-powershell
	// for more information.
	//
	// +kubebuilder:default:=Enabled
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// +optional
	EncryptionAtHost string `json:"encryptionAtHost,omitempty"`

	// DiskEncryptionSetID is the ID of the DiskEncryptionSet resource to use to encrypt the OS disks for the VMs. This
	// needs to exist in the same subscription id listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// DiskEncryptionSetID should also exist in a resource group under the same subscription id and the same location
	// listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.Location.
	//
	// +optional
	DiskEncryptionSetID string `json:"diskEncryptionSetID,omitempty"`

	// EnableEphemeralOSDisk is a flag when set to true, will enable ephemeral OS disk.
	//
	// +optional
	EnableEphemeralOSDisk bool `json:"enableEphemeralOSDisk,omitempty"`

	// SubnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same HostedCluster.Spec.Platform.Azure.VnetID and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
	SubnetID string `json:"subnetID"`

	// Diagnostics specifies the diagnostics settings for a virtual machine.
	// If not specified, then Boot diagnostics will be disabled.
	// +optional
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`

	// MachineIdentityID is a user-assigned identity assigned to the VMs used to authenticate with Azure services. This
	// field is expected to exist under the same resource group as HostedCluster.Spec.Platform.Azure.ResourceGroupName. This
	// user assigned identity is expected to have the Contributor role assigned to it and scoped to the resource group
	// under HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// If this field is not supplied, the Service Principal credentials will be written to a file on the disk of each VM
	// in order to be accessible by the cloud provider; the aforementioned credentials provided are the same ones as
	// HostedCluster.Spec.Platform.Azure.Credentials. However, this is less secure than using a managed identity.
	//
	// +optional
	MachineIdentityID string `json:"machineIdentityID,omitempty"`
}

// MarketplaceImage specifies the information needed to create an Azure VM from an Azure Marketplace image. This struct
// replicates the same fields found in CAPZ - https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/main/api/v1beta1/types.go.
type MarketplaceImage struct {
	// Publisher is the name of the organization that created the image
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-_]{2,49}$`
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=50
	Publisher string `json:"publisher"`

	// Offer specifies the name of a group of related images created by the publisher.
	//
	// +kubebuilder:validation:MinLength=1
	Offer string `json:"offer"`

	// SKU specifies an instance of an offer, such as a major release of a distribution.
	// For example, 18.04-LTS, 2019-Datacenter
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9-_]+$`
	// +kubebuilder:validation:MinLength=1
	SKU string `json:"sku"`

	// Version specifies the version of an image sku. The allowed formats are Major.Minor.Build or 'latest'. Major,
	// Minor, and Build are decimal numbers. Specify 'latest' to use the latest version of an image available at
	// deployment time. Even if you use 'latest', the VM image will not automatically update after deploy time even if a
	// new version becomes available.
	//
	// +kubebuilder:validation:Pattern=`^[0-9]+\.[0-9]+\.[0-9]+$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	Version string `json:"version"`
}

// AzureDiagnosticsStorageAccountType specifies the type of storage account for storing Azure VM diagnostics data.
// +kubebuilder:validation:Enum=Managed;UserManaged;Disabled
type AzureDiagnosticsStorageAccountType string

func (a *AzureDiagnosticsStorageAccountType) String() string {
	return string(*a)
}

func (a *AzureDiagnosticsStorageAccountType) Set(s string) error {
	switch s {
	case string(AzureDiagnosticsStorageAccountTypeDisabled), string(AzureDiagnosticsStorageAccountTypeManaged), string(AzureDiagnosticsStorageAccountTypeUserManaged):
		*a = AzureDiagnosticsStorageAccountType(s)
		return nil
	default:
		return fmt.Errorf("unknown Azure diagnostics storage account type: %s", s)
	}
}

func (a *AzureDiagnosticsStorageAccountType) Type() string {
	return "AzureDiagnosticsStorageAccountType"
}

const (
	AzureDiagnosticsStorageAccountTypeDisabled    = AzureDiagnosticsStorageAccountType("Disabled")
	AzureDiagnosticsStorageAccountTypeManaged     = AzureDiagnosticsStorageAccountType("Managed")
	AzureDiagnosticsStorageAccountTypeUserManaged = AzureDiagnosticsStorageAccountType("UserManaged")
)

// Diagnostics specifies the diagnostics settings for a virtual machine.
// +kubebuilder:validation:XValidation:rule="self.storageAccountType == 'UserManaged' ? has(self.storageAccountURI) : true", message="storageAccountURI is required when storageAccountType is UserManaged"
type Diagnostics struct {
	// StorageAccountType determines if the storage account for storing the diagnostics data
	// should be disabled (Disabled), provisioned by Azure (Managed) or by the user (UserManaged).
	// +kubebuilder:default:=Disabled
	StorageAccountType AzureDiagnosticsStorageAccountType `json:"storageAccountType,omitempty"`
	// StorageAccountURI is the URI of the user-managed storage account.
	// The URI typically will be `https://<mystorageaccountname>.blob.core.windows.net/`
	// but may differ if you are using Azure DNS zone endpoints.
	// You can find the correct endpoint by looking for the Blob Primary Endpoint in the
	// endpoints tab in the Azure console or with the CLI by issuing
	// `az storage account list --query='[].{name: name, "resource group": resourceGroup, "blob endpoint": primaryEndpoints.blob}'`.
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	StorageAccountURI string `json:"storageAccountURI,omitempty"`
}

// AzurePlatformSpec specifies configuration for clusters running on Azure. Generally, the HyperShift API assumes bring
// your own (BYO) cloud infrastructure resources. For example, resources like a resource group, a subnet, or a vnet
// would be pre-created and then their names would be used respectively in the ResourceGroupName, SubnetName, VnetName
// fields of the Hosted Cluster CR. An existing cloud resource is expected to exist under the same SubscriptionID.
type AzurePlatformSpec struct {
	// Credentials is the object containing existing Azure credentials needed for creating and managing cloud
	// infrastructure resources.
	//
	// +kubebuilder:validation:Required
	// +required
	Credentials corev1.LocalObjectReference `json:"credentials"`

	// Cloud is the cloud environment identifier, valid values could be found here: https://github.com/Azure/go-autorest/blob/4c0e21ca2bbb3251fe7853e6f9df6397f53dd419/autorest/azure/environments.go#L33
	//
	// +kubebuilder:validation:Enum=AzurePublicCloud;AzureUSGovernmentCloud;AzureChinaCloud;AzureGermanCloud;AzureStackCloud
	// +kubebuilder:default="AzurePublicCloud"
	Cloud string `json:"cloud,omitempty"`

	// Location is the Azure region in where all the cloud infrastructure resources will be created.
	//
	// Example: eastus
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Location is immutable"
	// +immutable
	// +required
	Location string `json:"location"`

	// ResourceGroupName is the name of an existing resource group where all cloud resources created by the Hosted
	// Cluster are to be placed. The resource group is expected to exist under the same subscription as SubscriptionID.
	//
	// In ARO HCP, this will be the managed resource group where customer cloud resources will be created.
	//
	// Resource group naming requirements can be found here: https://azure.github.io/PSRule.Rules.Azure/en/rules/Azure.ResourceGroup.Name/.
	//
	//Example: if your resource group ID is /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>, your
	//          ResourceGroupName is <resourceGroupName>.
	//
	// +kubebuilder:default:=default
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_()\-\.]{1,89}[a-zA-Z0-9_()\-]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ResourceGroupName is immutable"
	// +immutable
	// +required
	ResourceGroupName string `json:"resourceGroup"`

	// VnetID is the ID of an existing VNET to use in creating VMs. The VNET can exist in a different resource group
	// other than the one specified in ResourceGroupName, but it must exist under the same subscription as
	// SubscriptionID.
	//
	// In ARO HCP, this will be the ID of the customer provided VNET.
	//
	// Example: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="VnetID is immutable"
	// +immutable
	// +required
	VnetID string `json:"vnetID,omitempty"`

	// SubnetID is the subnet ID of an existing subnet where the load balancer for node egress will be created. This
	// subnet is expected to be a subnet within the VNET specified in VnetID. This subnet is expected to exist under the
	// same subscription as SubscriptionID.
	//
	// In ARO HCP, managed services will create the aforementioned load balancer in ResourceGroupName.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
	// +immutable
	// +required
	SubnetID string `json:"subnetID"`

	// SubscriptionID is a unique identifier for an Azure subscription used to manage resources.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SubscriptionID is immutable"
	// +immutable
	// +required
	SubscriptionID string `json:"subscriptionID"`

	// SecurityGroupID is the ID of an existing security group on the SubnetID. This field is provided as part of the
	// configuration for the Azure cloud provider, aka Azure cloud controller manager (CCM). This security group is
	// expected to exist under the same subscription as SubscriptionID.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SecurityGroupID is immutable"
	// +kubebuilder:validation:Required
	// +immutable
	// +required
	SecurityGroupID string `json:"securityGroupID"`

	// managedIdentities contains the managed identities needed for HCP control plane and data plane components that
	// authenticate with Azure's API.
	//
	// +kubebuilder:validation:Required
	ManagedIdentities AzureResourceManagedIdentities `json:"managedIdentities,omitempty"`
}

// ManagedAzureKeyVault is an Azure Key Vault on the management cluster.
type ManagedAzureKeyVault struct {
	// name is the name of the Azure Key Vault on the management cluster.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// tenantID is the tenant ID of the Azure Key Vault on the management cluster.
	//
	// +kubebuilder:validation:Required
	TenantID string `json:"tenantID"`
}

// AzureResourceManagedIdentities contains the managed identities needed for HCP control plane and data plane components
// that authenticate with Azure's API.
type AzureResourceManagedIdentities struct {
	// controlPlane contains the client IDs of all the managed identities on the HCP control plane needing to
	// authenticate with Azure's API.
	//
	// +kubebuilder:validation:Required
	ControlPlane ControlPlaneManagedIdentities `json:"controlPlane"`

	// Future placeholder - DataPlaneMIs * DataPlaneManagedIdentities
}

// ManagedIdentity contains the client ID, and its certificate name, of a managed identity. This managed identity is
// used, by an HCP component, to authenticate with the Azure API.
type ManagedIdentity struct {
	// clientID is the client ID of a managed identity.
	//
	// +kubebuilder:validation:XValidation:rule="self.matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the client ID of a managed identity must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12."
	// +kubebuilder:validation:Required
	ClientID string `json:"clientID"`

	// certificateName is the name of the certificate backing the managed identity. This certificate is expected to
	// reside in an Azure Key Vault on the management cluster.
	//
	// +kubebuilder:validation:Required
	CertificateName string `json:"certificateName"`
}

// ControlPlaneManagedIdentities contains the managed identities on the HCP control plane needing to authenticate with
// Azure's API.
type ControlPlaneManagedIdentities struct {
	// managedIdentitiesKeyVault contains information on the management cluster's managed identities Azure Key Vault.
	// This Key Vault is where the managed identities certificates are stored. These certificates are pulled out of the
	// Key Vault by the Secrets Store CSI driver and mounted into a volume on control plane pods requiring
	// authentication with Azure API.
	//
	// More information on how the Secrets Store CSI driver works to do this can be found here:
	// https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-driver.
	//
	// +kubebuilder:validation:Required
	ManagedIdentitiesKeyVault ManagedAzureKeyVault `json:"managedIdentitiesKeyVault"`

	// cloudProvider is a pre-existing managed identity associated with the azure cloud provider, aka cloud controller
	// manager.
	//
	// +kubebuilder:validation:Required
	CloudProvider ManagedIdentity `json:"cloudProvider"`

	// nodePoolManagement is a pre-existing managed identity associated with the operator managing the NodePools.
	//
	// +kubebuilder:validation:Required
	NodePoolManagement ManagedIdentity `json:"nodePoolManagement"`

	// controlPlaneOperator is a pre-existing managed identity associated with the control plane operator.
	//
	// +kubebuilder:validation:Required
	ControlPlaneOperator ManagedIdentity `json:"controlPlaneOperator"`

	// imageRegistry is a pre-existing managed identity associated with the cluster-image-registry-operator.
	//
	// +kubebuilder:validation:Required
	ImageRegistry ManagedIdentity `json:"imageRegistry"`

	// ingress is a pre-existing managed identity associated with the cluster-ingress-operator.
	//
	// +kubebuilder:validation:Required
	Ingress ManagedIdentity `json:"ingress"`

	// network is a pre-existing managed identity associated with the cluster-network-operator.
	//
	// +kubebuilder:validation:Required
	Network ManagedIdentity `json:"network"`

	// diskClientID is a pre-existing managed identity associated with the azure-disk-controller.
	//
	// +kubebuilder:validation:Required
	Disk ManagedIdentity `json:"disk"`

	// fileClientID is a pre-existing managed identity associated with the azure-disk-controller.
	//
	// +kubebuilder:validation:Required
	File ManagedIdentity `json:"file"`
}

// AzureKMSSpec defines metadata about the configuration of the Azure KMS Secret Encryption provider using Azure key vault
type AzureKMSSpec struct {
	// ActiveKey defines the active key used to encrypt new secrets
	//
	// +kubebuilder:validation:Required
	ActiveKey AzureKMSKey `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AzureKMSKey `json:"backupKey,omitempty"`

	// kms is a pre-existing managed identity used to authenticate with Azure KMS.
	//
	// +kubebuilder:validation:Required
	KMS ManagedIdentity `json:"kms"`
}

type AzureKMSKey struct {
	// KeyVaultName is the name of the keyvault. Must match criteria specified at https://docs.microsoft.com/en-us/azure/key-vault/general/about-keys-secrets-certificates#vault-name-and-object-name
	// Your Microsoft Entra application used to create the cluster must be authorized to access this keyvault, e.g using the AzureCLI:
	// `az keyvault set-policy -n $KEYVAULT_NAME --key-permissions decrypt encrypt --spn <YOUR APPLICATION CLIENT ID>`
	KeyVaultName string `json:"keyVaultName"`
	// KeyName is the name of the keyvault key used for encrypt/decrypt
	KeyName string `json:"keyName"`
	// KeyVersion contains the version of the key to use
	KeyVersion string `json:"keyVersion"`
}
