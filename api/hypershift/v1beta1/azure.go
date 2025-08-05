package v1beta1

import (
	"fmt"
)

// AzureVMImageType is used to specify the source of the Azure VM boot image.
// Valid values are ImageID and AzureMarketplace.
// +kubebuilder:validation:Enum:=ImageID;AzureMarketplace
type AzureVMImageType string

const (
	// ImageID is the used to specify that an Azure resource ID of a VHD image is used to boot the Azure VMs from.
	ImageID AzureVMImageType = "ImageID"

	// AzureMarketplace is used to specify the Azure Marketplace image info to use to boot the Azure VMs from.
	AzureMarketplace AzureVMImageType = "AzureMarketplace"
)

// AzureNodePoolPlatform is the platform specific configuration for an Azure node pool.
type AzureNodePoolPlatform struct {
	// vmSize is the Azure VM instance type to use for the nodes being created in the nodepool.
	// The size naming convention is documented here https://learn.microsoft.com/en-us/azure/virtual-machines/vm-naming-conventions.
	// Size names should start with a Family name, which is represented by one of more capital letters, and then be followed by the CPU count.
	// This is followed by 0 or more additional features, represented by a, b, d, i, l, m, p, t, s, C, and NP, refer to the Azure documentation for an explanation of these features.
	// Optionally an accelerator such as a GPU can be added, prefixed by an underscore, for example A100, H100 or MI300X.
	// The size may also be versioned, in which case it should be suffixed with _v<version> where the version is a number.
	// For example, "D32ads_v5" would be a suitable general purpose VM size, or "ND96_MI300X_v5" would represent a GPU accelerated VM.
	//
	// + Azure VM size format described in https://learn.microsoft.com/en-us/azure/virtual-machines/vm-naming-conventions
	// + "[A-Z]+[0-9]+(-[0-9]+)?" - Series, size and constrained CPU size
	// + "[abdilmptsCNP]*" - Additive features
	// + "(_[A-Z]*[0-9]+[A-Z]*)?" - Optional accelerator types
	// +kubebuilder:validation:Pattern=`^(Standard_|Basic_)?[A-Z]+[0-9]+(-[0-9]+)?[abdilmptsCNP]*(_[A-Z]*[0-9]+[A-Z]*)?(_v[0-9]+)?$`
	// +required
	// +kubebuilder:validation:MaxLength=255
	VMSize string `json:"vmSize"`

	// image is used to configure the VM boot image. If unset, the default image at the location below will be used and
	// is expected to exist: subscription/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Compute/images/rhcos.x86_64.vhd.
	// The <subscriptionID> and the <resourceGroupName> are expected to be the same resource group documented in the
	// Hosted Cluster specification respectively, HostedCluster.Spec.Platform.Azure.SubscriptionID and
	// HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// +required
	Image AzureVMImage `json:"image"`

	// osDisk provides configuration for the OS disk for the nodepool.
	// This can be used to configure the size, storage account type, encryption options and whether the disk is persistent or ephemeral.
	// When not provided, the platform will choose reasonable defaults which are subject to change over time.
	// Review the fields within the osDisk for more details.
	// +required
	OSDisk AzureNodePoolOSDisk `json:"osDisk"`

	// availabilityZone is the failure domain identifier where the VM should be attached to. This must not be specified
	// for clusters in a location that does not support AvailabilityZone because it would cause a failure from Azure API.
	// +kubebuilder:validation:XValidation:rule="self in ['1', '2', '3']"
	// +optional
	// +kubebuilder:validation:MaxLength=255
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// encryptionAtHost enables encryption at host on virtual machines. According to Microsoft documentation, this
	// means data stored on the VM host is encrypted at rest and flows encrypted to the Storage service. See
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disks-enable-host-based-encryption-portal?tabs=azure-powershell
	// for more information.
	//
	// +kubebuilder:default=Enabled
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// +optional
	EncryptionAtHost string `json:"encryptionAtHost,omitempty"`

	// subnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same network, HostedCluster.Spec.Platform.Azure.VnetID, and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// subnetID is immutable once set.
	// The subnetID should be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`.
	// The subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12.
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis and must not end with a period (.) character.
	// The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods and must not end with either a period (.) or hyphen (-) character.
	// The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character and must not end with a period (.) or hyphen (-) character.
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 11 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Network/virtualNetworks/.*/subnets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the subnetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[8].matches('[a-zA-Z0-9-_\\.]{2,64}')`,message="The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[8].endsWith('.') && !self.split('/')[8].endsWith('-')",message="the vnetName in the subnetID must not end with either a period (.) or hyphen (-) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[10].matches('[a-zA-Z0-9][a-zA-Z0-9-_\\.]{0,79}')`,message="The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[10].endsWith('.') && !self.split('/')[10].endsWith('-')",message="the subnetName in the subnetID must not end with a period (.) or hyphen (-) character"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +required
	SubnetID string `json:"subnetID"`

	// diagnostics specifies the diagnostics settings for a virtual machine.
	// If not specified, then Boot diagnostics will be disabled.
	// +optional
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`
}

// AzureVMImage represents the different types of boot image sources that can be provided for an Azure VM.
// +kubebuilder:validation:XValidation:rule="has(self.type) && self.type == 'ImageID' ?  has(self.imageID) : !has(self.imageID)",message="imageID is required when type is ImageID, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="has(self.type) && self.type == 'AzureMarketplace' ?  has(self.azureMarketplace) : !has(self.azureMarketplace)",message="azureMarketplace is required when type is RequiredMember, and forbidden otherwise"
// +union
type AzureVMImage struct {
	// type is the type of image data that will be provided to the Azure VM.
	// Valid values are "ImageID" and "AzureMarketplace".
	// ImageID means is used for legacy managed VM images. This is where the user uploads a VM image directly to their resource group.
	// AzureMarketplace means the VM will boot from an Azure Marketplace image.
	// Marketplace images are preconfigured and published by the OS vendors and may include preconfigured software for the VM.
	//
	// +required
	// +unionDiscriminator
	Type AzureVMImageType `json:"type"`

	// imageID is the Azure resource ID of a VHD image to use to boot the Azure VMs from.
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +optional
	// +unionMember
	// +kubebuilder:validation:MaxLength=255
	ImageID *string `json:"imageID,omitempty"`

	// azureMarketplace contains the Azure Marketplace image info to use to boot the Azure VMs from.
	//
	// +optional
	// +unionMember
	AzureMarketplace *AzureMarketplaceImage `json:"azureMarketplace,omitempty"`
}

// AzureMarketplaceImage specifies the information needed to create an Azure VM from an Azure Marketplace image.
// + This struct replicates the same fields found in CAPZ - https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/main/api/v1beta1/types.go.
type AzureMarketplaceImage struct {
	// publisher is the name of the organization that created the image.
	// It must be between 3 and 50 characters in length, and consist of only lowercase letters, numbers, and hyphens (-) and underscores (_).
	// It must start with a lowercase letter or a number.
	// TODO: Can we explain where a user might find this value, or provide an example of one they might want to use
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-_]{2,49}$`
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=50
	// +required
	Publisher string `json:"publisher"`

	// offer specifies the name of a group of related images created by the publisher.
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	Offer string `json:"offer"`

	// sku specifies an instance of an offer, such as a major release of a distribution.
	// For example, 22_04-lts-gen2, 8-lvm-gen2.
	// The value must consist only of lowercase letters, numbers, and hyphens (-) and underscores (_).
	// TODO: What about length limits?
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9-_]+$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	SKU string `json:"sku"`

	// version specifies the version of an image sku. The allowed formats are Major.Minor.Build or 'latest'. Major,
	// Minor, and Build are decimal numbers, e.g. '1.2.0'. Specify 'latest' to use the latest version of an image available at
	// deployment time. Even if you use 'latest', the VM image will not automatically update after deploy time even if a
	// new version becomes available.
	//
	// +kubebuilder:validation:Pattern=`^[0-9]+\.[0-9]+\.[0-9]+$|^latest$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +required
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
// +kubebuilder:validation:XValidation:rule="self.storageAccountType == 'UserManaged' ? has(self.userManaged) : !has(self.userManaged)", message="userManaged is required when storageAccountType is UserManaged, and forbidden otherwise"
// +union
type Diagnostics struct {
	// storageAccountType determines if the storage account for storing the diagnostics data
	// should be disabled (Disabled), provisioned by Azure (Managed) or by the user (UserManaged).
	// +kubebuilder:validation:Enum=Managed;UserManaged;Disabled
	// +kubebuilder:default:=Disabled
	// +unionDiscriminator
	// +optional
	StorageAccountType AzureDiagnosticsStorageAccountType `json:"storageAccountType,omitempty"`

	// userManaged specifies the diagnostics settings for a virtual machine when the storage account is managed by the user.
	// +optional
	// +unionMember
	UserManaged *UserManagedDiagnostics `json:"userManaged,omitempty"`
}

// UserManagedDiagnostics specifies the diagnostics settings for a virtual machine when the storage account is managed by the user.
type UserManagedDiagnostics struct {
	// storageAccountURI is the URI of the user-managed storage account.
	// The URI typically will be `https://<mystorageaccountname>.blob.core.windows.net/`
	// but may differ if you are using Azure DNS zone endpoints.
	// You can find the correct endpoint by looking for the Blob Primary Endpoint in the
	// endpoints tab in the Azure console or with the CLI by issuing
	// `az storage account list --query='[].{name: name, "resource group": resourceGroup, "blob endpoint": primaryEndpoints.blob}'`.
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https'", message="storageAccountURI must be a valid HTTPS URL"
	// +kubebuilder:validation:MaxLength=1024
	// +required
	StorageAccountURI string `json:"storageAccountURI"`
}

// +kubebuilder:validation:Enum=Premium_LRS;PremiumV2_LRS;Standard_LRS;StandardSSD_LRS;UltraSSD_LRS
type AzureDiskStorageAccountType string

// Values copied from https://github.com/openshift/cluster-api-provider-azure/blob/release-4.18/vendor/github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5/constants.go#L614
// excluding zone redundant storage(ZRS) types as they are not available in all regions.
const (
	// DiskStorageAccountTypesPremiumLRS - Premium SSD locally redundant storage. Best for production and performance sensitive
	// workloads.
	DiskStorageAccountTypesPremiumLRS AzureDiskStorageAccountType = "Premium_LRS"
	// DiskStorageAccountTypesPremiumV2LRS - Premium SSD v2 locally redundant storage. Best for production and performance-sensitive
	// workloads that consistently require low latency and high IOPS and throughput.
	DiskStorageAccountTypesPremiumV2LRS AzureDiskStorageAccountType = "PremiumV2_LRS"
	// DiskStorageAccountTypesStandardLRS - Standard HDD locally redundant storage. Best for backup, non-critical, and infrequent
	// access.
	DiskStorageAccountTypesStandardLRS AzureDiskStorageAccountType = "Standard_LRS"
	// DiskStorageAccountTypesStandardSSDLRS - Standard SSD locally redundant storage. Best for web servers, lightly used enterprise
	// applications and dev/test.
	DiskStorageAccountTypesStandardSSDLRS AzureDiskStorageAccountType = "StandardSSD_LRS"
	// DiskStorageAccountTypesUltraSSDLRS - Ultra SSD locally redundant storage. Best for IO-intensive workloads such as SAP HANA,
	// top tier databases (for example, SQL, Oracle), and other transaction-heavy workloads.
	DiskStorageAccountTypesUltraSSDLRS AzureDiskStorageAccountType = "UltraSSD_LRS"
)

// +kubebuilder:validation:Enum=Persistent;Ephemeral
type AzureDiskPersistence string

const (
	// PersistentDiskPersistence is the persistent disk type.
	PersistentDiskPersistence AzureDiskPersistence = "Persistent"

	// EphemeralDiskPersistence is the ephemeral disk type.
	EphemeralDiskPersistence AzureDiskPersistence = "Ephemeral"
)

// +kubebuilder:validation:XValidation:rule="!has(self.diskStorageAccountType) || self.diskStorageAccountType != 'UltraSSD_LRS' || self.sizeGiB <= 32767",message="When not using diskStorageAccountType UltraSSD_LRS, the SizeGB value must be less than or equal to 32,767"
type AzureNodePoolOSDisk struct {
	// sizeGiB is the size in GiB (1024^3 bytes) to assign to the OS disk.
	// This should be between 16 and 65,536 when using the UltraSSD_LRS storage account type and between 16 and 32,767 when using any other storage account type.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is 30.
	//
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=65536
	// +optional
	SizeGiB int32 `json:"sizeGiB,omitempty"`

	// diskStorageAccountType is the disk storage account type to use.
	// Valid values are Premium_LRS, PremiumV2_LRS, Standard_LRS, StandardSSD_LRS, UltraSSD_LRS.
	// Note that Standard means a HDD.
	// The disk performance is tied to the disk type, please refer to the Azure documentation for further details
	// https://docs.microsoft.com/en-us/azure/virtual-machines/disks-types#disk-type-comparison.
	// When omitted this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is Premium SSD LRS.
	//
	// +optional
	DiskStorageAccountType AzureDiskStorageAccountType `json:"diskStorageAccountType,omitempty"`

	// encryptionSetID is the ID of the DiskEncryptionSet resource to use to encrypt the OS disks for the VMs.
	// Configuring a DiskEncyptionSet allows greater control over the encryption of the VM OS disk at rest.
	// Can be used with either platform (Azure) managed, or customer managed encryption keys.
	// This needs to exist in the same subscription id listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// DiskEncryptionSetID should also exist in a resource group under the same subscription id and the same location
	// listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.Location.
	// The encryptionSetID should be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Copmute/diskEncryptionSets/{resourceName}`.
	// The subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12.
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis and must not end with a period (.) character.
	// The resourceName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores.
	// TODO: Are there other encryption related options we may want to expose, should this be in a struct as well?
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 9 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Compute/diskEncryptionSets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/diskEncryptionSets/{resourceName}`"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the encryptionSetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[8].matches('[a-zA-Z0-9-_]{1,80}')",message="The resourceName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=285
	// +optional
	EncryptionSetID string `json:"encryptionSetID,omitempty"`

	// persistence determines whether the OS disk should be persisted beyond the life of the VM.
	// Valid values are Persistent and Ephemeral.
	// When set to Ephmeral, the OS disk will not be persisted to Azure storage and implies restrictions to the VM size and caching type.
	// Full details can be found in the Azure documentation https://learn.microsoft.com/en-us/azure/virtual-machines/ephemeral-os-disks.
	// Ephmeral disks are primarily used for stateless applications, provide lower latency than Persistent disks and also incur no storage costs.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	//
	// +optional
	Persistence AzureDiskPersistence `json:"persistence,omitempty"`
}

// AzurePlatformSpec specifies configuration for clusters running on Azure. Generally, the HyperShift API assumes bring
// your own (BYO) cloud infrastructure resources. For example, resources like a resource group, a subnet, or a vnet
// would be pre-created and then their names would be used respectively in the ResourceGroupName, SubnetName, VnetName
// fields of the Hosted Cluster CR. An existing cloud resource is expected to exist under the same SubscriptionID.
type AzurePlatformSpec struct {
	// cloud is the cloud environment identifier, valid values could be found here: https://github.com/Azure/go-autorest/blob/4c0e21ca2bbb3251fe7853e6f9df6397f53dd419/autorest/azure/environments.go#L33
	//
	// +kubebuilder:validation:Enum=AzurePublicCloud;AzureUSGovernmentCloud;AzureChinaCloud;AzureGermanCloud;AzureStackCloud
	// +kubebuilder:default="AzurePublicCloud"
	// +optional
	Cloud string `json:"cloud,omitempty"`

	// location is the Azure region in where all the cloud infrastructure resources will be created.
	//
	// Example: eastus
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Location is immutable"
	// +kubebuilder:validation:MaxLength=255
	// +immutable
	Location string `json:"location"`

	// resourceGroup is the name of an existing resource group where all cloud resources created by the Hosted
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
	// +required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_()\-\.]{1,89}[a-zA-Z0-9_()\-]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ResourceGroupName is immutable"
	// +kubebuilder:validation:MaxLength=90
	// +immutable
	ResourceGroupName string `json:"resourceGroup"`

	// vnetID is the ID of an existing VNET to use in creating VMs. The VNET can exist in a different resource group
	// other than the one specified in ResourceGroupName, but it must exist under the same subscription as
	// SubscriptionID.
	//
	// In ARO HCP, this will be the ID of the customer provided VNET.
	//
	// Example: /subscriptions/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Network/virtualNetworks/<vnetName>
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="VnetID is immutable"
	// +kubebuilder:validation:MaxLength=255
	// +immutable
	VnetID string `json:"vnetID"`

	// subnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same network, HostedCluster.Spec.Platform.Azure.VnetID, and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// subnetID is immutable once set.
	// The subnetID should be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`.
	// The subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12.
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis and must not end with a period (.) character.
	// The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods and must not end with either a period (.) or hyphen (-) character.
	// The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character and must not end with a period (.) or hyphen (-) character.
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 11 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Network/virtualNetworks/.*/subnets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`"
	// +kubeubilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and parenthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the subnetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[8].matches('[a-zA-Z0-9-_\\.]{2,64}')`,message="The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[8].endsWith('.') && !self.split('/')[8].endsWith('-')",message="the vnetName in the subnetID must not end with either a period (.) or hyphen (-) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[10].matches('[a-zA-Z0-9][a-zA-Z0-9-_\\.]{0,79}')`,message="The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[10].endsWith('.') && !self.split('/')[10].endsWith('-')",message="the subnetName in the subnetID must not end with a period (.) or hyphen (-) character"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +required
	SubnetID string `json:"subnetID"`

	// subscriptionID is a unique identifier for an Azure subscription used to manage resources.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SubscriptionID is immutable"
	// +kubebuilder:validation:MaxLength=255
	// +immutable
	SubscriptionID string `json:"subscriptionID"`

	// securityGroupID is the ID of an existing security group on the SubnetID. This field is provided as part of the
	// configuration for the Azure cloud provider, aka Azure cloud controller manager (CCM). This security group is
	// expected to exist under the same subscription as SubscriptionID.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="SecurityGroupID is immutable"
	// +required
	// +kubebuilder:validation:MaxLength=255
	// +immutable
	SecurityGroupID string `json:"securityGroupID"`

	// azureAuthenticationConfig is the type of Azure authentication configuration to use to authenticate with Azure's
	// Cloud API.
	//
	// +required
	AzureAuthenticationConfig AzureAuthenticationConfiguration `json:"azureAuthenticationConfig"`

	// tenantID is a unique identifier for the tenant where Azure resources will be created and managed in.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	TenantID string `json:"tenantID"`
}

// objectEncoding represents the encoding for the Azure Key Vault secret containing the certificate related to
// CertificateName. objectEncoding needs to match the encoding format used when the certificate was stored in the
// Azure Key Vault. If objectEncoding doesn't match the encoding format of the certificate, the certificate will
// unsuccessfully be read by the Secrets CSI driver and an error will occur. This error will only be visible on the
// SecretProviderClass custom resource related to the managed identity.
//
// The default value is utf-8.
//
// See this for more info - https://github.com/Azure/secrets-store-csi-driver-provider-azure/blob/master/website/content/en/getting-started/usage/_index.md
//
// +kubebuilder:validation:Enum:=utf-8;hex;base64
// +kubebuilder:default:="utf-8"
type ObjectEncodingFormat string

// ManagedAzureKeyVault is an Azure Key Vault on the management cluster.
type ManagedAzureKeyVault struct {
	// name is the name of the Azure Key Vault on the management cluster.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// tenantID is the tenant ID of the Azure Key Vault on the management cluster.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	TenantID string `json:"tenantID"`
}

// AzureResourceManagedIdentities contains the managed identities needed for HCP control plane and data plane components
// that authenticate with Azure's API.
type AzureResourceManagedIdentities struct {
	// controlPlane contains the client IDs of all the managed identities on the HCP control plane needing to
	// authenticate with Azure's API.
	//
	// +required
	ControlPlane ControlPlaneManagedIdentities `json:"controlPlane"`

	// dataPlane contains the client IDs of all the managed identities on the data plane needing to authenticate with
	// Azure's API.
	//
	// +required
	DataPlane DataPlaneManagedIdentities `json:"dataPlane"`
}

// AzureClientID is a string that represents the client ID of a managed identity.
//
// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$')",message="the client ID of a managed identity must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12."
// +kubebuilder:validation:MinLength=36
// +kubebuilder:validation:MaxLength=36
// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}$`
type AzureClientID string

// AzureWorkloadIdentities is a struct that contains the client IDs of all the managed identities in self-managed Azure
// needing to authenticate with Azure's API.
type AzureWorkloadIdentities struct {
	// imageRegistry is the client ID of a federated managed identity, associated with cluster-image-registry-operator, used in
	// workload identity authentication.
	// +required
	ImageRegistry WorkloadIdentity `json:"imageRegistry"`

	// ingress is the client ID of a federated managed identity, associated with cluster-ingress-operator, used in
	// workload identity authentication.
	// +required
	Ingress WorkloadIdentity `json:"ingress"`

	// file is the client ID of a federated managed identity, associated with cluster-storage-operator-file,
	// used in workload identity authentication.
	// +required
	File WorkloadIdentity `json:"file"`

	// disk is the client ID of a federated managed identity, associated with cluster-storage-operator-disk,
	// used in workload identity authentication.
	// +required
	Disk WorkloadIdentity `json:"disk"`

	// nodePoolManagement is the client ID of a federated managed identity, associated with cluster-api-provider-azure, used
	// in workload identity authentication.
	// +required
	NodePoolManagement WorkloadIdentity `json:"nodePoolManagement"`

	// cloudProvider is the client ID of a federated managed identity, associated with azure-cloud-provider, used in
	// workload identity authentication.
	// +required
	CloudProvider WorkloadIdentity `json:"cloudProvider"`

	// network is the client ID of a federated managed identity, associated with cluster-network-operator, used in
	// workload identity authentication.
	// +required
	Network WorkloadIdentity `json:"network"`
}

// ManagedIdentity contains the client ID, and its certificate name, of a managed identity. This managed identity is
// used, by an HCP component, to authenticate with the Azure API.
type ManagedIdentity struct {
	// clientID is the client ID of a managed identity associated with CredentialsSecretName. This field is optional and
	// mainly used for CI purposes.
	//
	// +optional
	ClientID AzureClientID `json:"clientID,omitempty"`

	// objectEncoding represents the encoding for the Azure Key Vault secret containing the certificate related to
	// the managed identity. objectEncoding needs to match the encoding format used when the certificate was stored in the
	// Azure Key Vault. If objectEncoding doesn't match the encoding format of the certificate, the certificate will
	// unsuccessfully be read by the Secrets CSI driver and an error will occur. This error will only be visible on the
	// SecretProviderClass custom resource related to the managed identity.
	//
	// The default value is utf-8.
	//
	// See this for more info - https://github.com/Azure/secrets-store-csi-driver-provider-azure/blob/master/website/content/en/getting-started/usage/_index.md
	//
	// +kubebuilder:validation:Enum:=utf-8;hex;base64
	// +kubebuilder:default:="utf-8"
	// +required
	ObjectEncoding ObjectEncodingFormat `json:"objectEncoding"`

	// credentialsSecretName is the name of an Azure Key Vault secret. This field assumes the secret contains the JSON
	// format of a UserAssignedIdentityCredentials struct. At a minimum, the secret needs to contain the ClientId,
	// ClientSecret, AuthenticationEndpoint, NotBefore, and NotAfter, and TenantId.
	//
	// More info on this struct can be found here - https://github.com/Azure/msi-dataplane/blob/63fb37d3a1aaac130120624674df795d2e088083/pkg/dataplane/internal/generated_client.go#L156.
	//
	// credentialsSecretName must be between 1 and 127 characters and use only alphanumeric characters and hyphens.
	// credentialsSecretName must also be unique within the Azure Key Vault. See more details here - https://azure.github.io/PSRule.Rules.Azure/en/rules/Azure.KeyVault.SecretName/.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9-]+$`
	// +required
	CredentialsSecretName string `json:"credentialsSecretName"`
}

// WorkloadIdentity is a struct that contains the client ID of a federated managed identity used in workload identity
// authentication.
type WorkloadIdentity struct {
	// clientID is client ID of a federated managed identity used in workload identity authentication
	//
	// +required
	ClientID AzureClientID `json:"clientID"`
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
	// +required
	ManagedIdentitiesKeyVault ManagedAzureKeyVault `json:"managedIdentitiesKeyVault"`

	// cloudProvider is a pre-existing managed identity associated with the azure cloud provider, aka cloud controller
	// manager.
	//
	// +required
	CloudProvider ManagedIdentity `json:"cloudProvider"`

	// nodePoolManagement is a pre-existing managed identity associated with the operator managing the NodePools.
	//
	// +required
	NodePoolManagement ManagedIdentity `json:"nodePoolManagement"`

	// controlPlaneOperator is a pre-existing managed identity associated with the control plane operator.
	//
	// +required
	ControlPlaneOperator ManagedIdentity `json:"controlPlaneOperator"`

	// imageRegistry is a pre-existing managed identity associated with the cluster-image-registry-operator.
	//
	// +optional
	ImageRegistry ManagedIdentity `json:"imageRegistry"`

	// ingress is a pre-existing managed identity associated with the cluster-ingress-operator.
	//
	// +required
	Ingress ManagedIdentity `json:"ingress"`

	// network is a pre-existing managed identity associated with the cluster-network-operator.
	//
	// +required
	Network ManagedIdentity `json:"network"`

	// disk is a pre-existing managed identity associated with the azure-disk-controller.
	//
	// +required
	Disk ManagedIdentity `json:"disk"`

	// file is a pre-existing managed identity associated with the azure-disk-controller.
	//
	// +required
	File ManagedIdentity `json:"file"`
}

// DataPlaneManagedIdentities contains the client IDs of all the managed identities on the data plane needing to
// authenticate with Azure's API.
type DataPlaneManagedIdentities struct {
	// imageRegistryMSIClientID is the client ID of a pre-existing managed identity ID associated with the image
	//registry controller.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	ImageRegistryMSIClientID string `json:"imageRegistryMSIClientID"`

	// diskMSIClientID is the client ID of a pre-existing managed identity ID associated with the CSI Disk driver.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	DiskMSIClientID string `json:"diskMSIClientID"`

	// fileMSIClientID is the client ID of a pre-existing managed identity ID associated with the CSI File driver.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	FileMSIClientID string `json:"fileMSIClientID"`
}

// AzureKMSSpec defines metadata about the configuration of the Azure KMS Secret Encryption provider using Azure key vault
type AzureKMSSpec struct {
	// activeKey defines the active key used to encrypt new secrets
	//
	// +required
	ActiveKey AzureKMSKey `json:"activeKey"`
	// backupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AzureKMSKey `json:"backupKey,omitempty"`

	// kms is a pre-existing managed identity used to authenticate with Azure KMS.
	//
	// +required
	KMS ManagedIdentity `json:"kms"`
}

type AzureKMSKey struct {
	// keyVaultName is the name of the keyvault. Must match criteria specified at https://docs.microsoft.com/en-us/azure/key-vault/general/about-keys-secrets-certificates#vault-name-and-object-name
	// Your Microsoft Entra application used to create the cluster must be authorized to access this keyvault, e.g using the AzureCLI:
	// `az keyvault set-policy -n $KEYVAULT_NAME --key-permissions decrypt encrypt --spn <YOUR APPLICATION CLIENT ID>`
	// +kubebuilder:validation:MaxLength=255
	// +required
	KeyVaultName string `json:"keyVaultName"`

	// keyName is the name of the keyvault key used for encrypt/decrypt
	// +kubebuilder:validation:MaxLength=255
	// +required
	KeyName string `json:"keyName"`

	// keyVersion contains the version of the key to use
	// +kubebuilder:validation:MaxLength=255
	// +required
	KeyVersion string `json:"keyVersion"`
}

// AzureAuthenticationType is a discriminated union type that contains the Azure authentication configuration for an
// Azure Hosted Cluster. This type is used to determine which authentication configuration is being used. Valid values
// are "ManagedIdentities" and "WorkloadIdentities".
//
// +kubebuilder:validation:Enum=ManagedIdentities;WorkloadIdentities
type AzureAuthenticationType string

const (
	// "ManagedIdentities" means that the Hosted Cluster is using managed identities to authenticate with Azure's API.
	// This is only valid for managed Azure, also known as ARO HCP.
	AzureAuthenticationTypeManagedIdentities AzureAuthenticationType = "ManagedIdentities"

	// "WorkloadIdentities" means that the Hosted Cluster is using workload identities to authenticate with Azure's API.
	// This is only valid for self-managed Azure.
	AzureAuthenticationTypeWorkloadIdentities AzureAuthenticationType = "WorkloadIdentities"
)

// azureAuthenticationConfiguration is a discriminated union type that contains the Azure authentication configuration
// for a Hosted Cluster. This configuration is used to determine how the Hosted Cluster authenticates with Azure's API,
// either with managed identities or workload identities.
//
// +kubebuilder:validation:XValidation:rule="self.azureAuthenticationConfigType == 'ManagedIdentities' ? has(self.managedIdentities) : !has(self.managedIdentities)", message="managedIdentities is required when azureAuthenticationConfigType is ManagedIdentities, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.azureAuthenticationConfigType == 'WorkloadIdentities' ? has(self.workloadIdentities) : !has(self.workloadIdentities)", message="workloadIdentities is required when azureAuthenticationConfigType is WorkloadIdentities, and forbidden otherwise"
// +union
type AzureAuthenticationConfiguration struct {
	// azureAuthenticationConfigType is the type of identity configuration used in the Hosted Cluster. This field is
	// used to determine which identity configuration is being used. Valid values are "ManagedIdentities" and
	// "WorkloadIdentities".
	//
	// +unionDiscriminator
	// +required
	AzureAuthenticationConfigType AzureAuthenticationType `json:"azureAuthenticationConfigType"`

	// managedIdentities contains the managed identities needed for HCP control plane and data plane components that
	// authenticate with Azure's API.
	//
	// These are required for managed Azure, also known as ARO HCP.
	//
	// +optional
	ManagedIdentities *AzureResourceManagedIdentities `json:"managedIdentities,omitempty"`

	// workloadIdentities is a struct of client IDs for each component that needs to authenticate with Azure's API in
	// self-managed Azure. These client IDs are used to authenticate with Azure cloud on both the control plane and data
	// plane.
	//
	// This is required for self-managed Azure.
	// +optional
	WorkloadIdentities *AzureWorkloadIdentities `json:"workloadIdentities,omitempty"`
}
