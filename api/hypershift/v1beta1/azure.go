package v1beta1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
	// +kubebuilder:validation:Pattern=`^(Standard_|Basic_)?[A-Z]+[0-9]+(-[0-9]+)?[abdilmptsCNP]*(_[A-Z]*[0-9]+[A-Z]*)?(_v[0-9]+)?$`
	// +kubebuilder:validation:Required
	// + Azure VM size format described in https://learn.microsoft.com/en-us/azure/virtual-machines/vm-naming-conventions
	// + "[A-Z]+[0-9]+(-[0-9]+)?" - Series, size and constrained CPU size
	// + "[abdilmptsCNP]*" - Additive features
	// + "(_[A-Z]*[0-9]+[A-Z]*)?" - Optional accelerator types
	VMSize string `json:"vmSize"`

	// image is used to configure the VM boot image. If unset, the default image at the location below will be used and
	// is expected to exist: subscription/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Compute/images/rhcos.x86_64.vhd.
	// The <subscriptionID> and the <resourceGroupName> are expected to be the same resource group documented in the
	// Hosted Cluster specification respectively, HostedCluster.Spec.Platform.Azure.SubscriptionID and
	// HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// +kubebuilder:validation:Required
	Image AzureVMImage `json:"image"`

	// osDisk provides configuration for the OS disk for the nodepool.
	// This can be used to configure the size, storage account type, encryption options and whether the disk is persistent or ephemeral.
	// When not provided, the platform will choose reasonable defaults which are subject to change over time.
	// Review the fields within the osDisk for more details.
	OSDisk AzureNodePoolOSDisk `json:"osDisk"`

	// availabilityZone is the failure domain identifier where the VM should be attached to. This must not be specified
	// for clusters in a location that does not support AvailabilityZone because it would cause a failure from Azure API.
	//kubebuilder:validation:XValidation:rule='availabilityZone in ["1", "2", "3"]'
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

	// subnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same network, HostedCluster.Spec.Platform.Azure.VnetID, and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// subnetID is immutable once set.
	// The subnetID should be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`.
	// The subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12.
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis and must not end with a period (.) character.
	// The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods and must not end with either a period (.) or hyphen (-) character.
	// The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character and must not end with a period (.) or hyphen (-) character.
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 11 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Network/virtualNetworks/.*/subnets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`"
	// +kubeubilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the subnetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[8].matches('[a-zA-Z0-9-_\\.]{2,64}')`,message="The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[8].endsWith('.') && !self.split('/')[8].endsWith('-')",message="the vnetName in the subnetID must not end with either a period (.) or hyphen (-) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[10].matches('[a-zA-Z0-9][a-zA-Z0-9-_\\.]{0,79}')`,message="The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[10].endsWith('.') && !self.split('/')[10].endsWith('-')",message="the subnetName in the subnetID must not end with a period (.) or hyphen (-) character"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
	SubnetID string `json:"subnetID"`

	// diagnostics specifies the diagnostics settings for a virtual machine.
	// If not specified, then Boot diagnostics will be disabled.
	// +optional
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`

	// machineIdentityID is a user-assigned identity assigned to the VMs used to authenticate with Azure services. The
	// identify is expected to exist under the same resource group as HostedCluster.Spec.Platform.Azure.ResourceGroupName. This
	// user assigned identity is expected to have the Contributor role assigned to it and scoped to the resource group
	// under HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// If this field is not supplied, the Service Principal credentials will be written to a file on the disk of each VM
	// in order to be accessible by the cloud provider; the aforementioned credentials provided are the same ones as
	// HostedCluster.Spec.Platform.Azure.Credentials. However, this is less secure than using a managed identity.
	//
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +optional
	MachineIdentityID string `json:"machineIdentityID,omitempty"`
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
	// +kubebuilder:validation:Required
	// +unionDiscriminator
	Type AzureVMImageType `json:"type"`

	// imageID is the Azure resource ID of a VHD image to use to boot the Azure VMs from.
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +optional
	// +unionMember
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
	// +kubeubilder:validation:Required
	Publisher string `json:"publisher"`

	// offer specifies the name of a group of related images created by the publisher.
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Offer string `json:"offer"`

	// sku specifies an instance of an offer, such as a major release of a distribution.
	// For example, 22_04-lts-gen2, 8-lvm-gen2.
	// The value must consist only of lowercase letters, numbers, and hyphens (-) and underscores (_).
	// TODO: What about length limits?
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9-_]+$`
	// +kubebuilder:validation:MinLength=1
	SKU string `json:"sku"`

	// version specifies the version of an image sku. The allowed formats are Major.Minor.Build or 'latest'. Major,
	// Minor, and Build are decimal numbers, e.g. '1.2.0'. Specify 'latest' to use the latest version of an image available at
	// deployment time. Even if you use 'latest', the VM image will not automatically update after deploy time even if a
	// new version becomes available.
	//
	// +kubebuilder:validation:Pattern=`^[0-9]+\.[0-9]+\.[0-9]+$|^latest$`
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
	// +kubebuilder:validation:Required
	StorageAccountURI string `json:"storageAccountURI,omitempty"`
}

// +kubebuilder:validation:Enum=Standard;StandardSSD;PremiumSSD;UltraSSD
type AzureDiskStorageAccountType string

const (
	// StandardStorageAccountType is the standard HDD storage account type.
	StandardStorageAccountType AzureDiskStorageAccountType = "Standard"

	// StandardSSDStorageAccountType is the standard SSD storage account type.
	StandardSSDStorageAccountType AzureDiskStorageAccountType = "StandardSSD"

	// PremiumSSDStorageAccountType is the premium SSD storage account type.
	PremiumSSDStorageAccountType AzureDiskStorageAccountType = "PremiumSSD"

	// UltraSSDStorageAccountType is the ultra SSD storage account type.
	UltraSSDStorageAccountType AzureDiskStorageAccountType = "UltraSSD"
)

// +kubebuilder:validation:Enum=Persistent;Ephemeral
type AzureDiskPersistence string

const (
	// PersistentDiskPersistence is the persistent disk type.
	PersistentDiskPersistence AzureDiskPersistence = "Persistent"

	// EphemeralDiskPersistence is the ephemeral disk type.
	EphemeralDiskPersistence AzureDiskPersistence = "Ephemeral"
)

// +kubebuilder:validation:XValidation:rule="!has(self.diskStorageAccountType) || self.diskStorageAccountType != 'UltraSSD' || self.sizeGiB <= 32767",message="When not using storageAccountType UltraSSD, the SizeGB value must be less than or equal to 32,767"
type AzureNodePoolOSDisk struct {
	// SizeGiB is the size in GiB (1024^3 bytes) to assign to the OS disk.
	// This should be between 16 and 65,536 when using the UltraSSD storage account type and between 16 and 32,767 when using any other storage account type.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is 30.
	//
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=65536
	// +optional
	SizeGiB int32 `json:"sizeGiB,omitempty"`

	// storageAccountType is the disk storage account type to use.
	// Valid values are Standard, StandardSSD, PremiumSSD and UltraSSD and omitted.
	// Note that Standard means a HDD.
	// The disk performance is tied to the disk type, please refer to the Azure documentation for further details
	// https://docs.microsoft.com/en-us/azure/virtual-machines/disks-types#disk-type-comparison.
	// When omitted this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is PremiumSSD.
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
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis and must not end with a period (.) character.
	// The resourceName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores.
	// TODO: Are there other encryption related options we may want to expose, should this be in a struct as well?
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 9 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Compute/diskEncryptionSets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Copmute/diskEncryptionSets/{resourceName}`"
	// +kubeubilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the encryptionSetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[8].matches('[a-zA-Z0-9-_]{1,80}')",message="The resourceName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores"
	// +kubeubilder:validation:MinLength:=1
	// +kubebuilder:validation:MaxLength:=285
	// +optional
	EncryptionSetID string `json:"encryptionSetID,omitempty"`

	// persistence determines whether the OS disk should be persisted beyond the life of the VM.
	// Valid values are Persistent and Ephemeral.
	// When set to Ephmeral, the OS disk will not be persisted to Azure storage and implies restrictions to the VM size and caching type.
	// Full details can be found in the Azure documentation https://learn.microsoft.com/en-us/azure/virtual-machines/ephemeral-os-disks.
	// Ephmeral disks are primarily used for stateless applications, provide lower latency than Persistent disks and also incur no storage costs.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is Persistent.
	//
	// +optional
	Persistence AzureDiskPersistence `json:"persistence,omitempty"`
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

	// subnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same network, HostedCluster.Spec.Platform.Azure.VnetID, and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// subnetID is immutable once set.
	// The subnetID should be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`.
	// The subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12.
	// The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis and must not end with a period (.) character.
	// The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods and must not end with either a period (.) or hyphen (-) character.
	// The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character and must not end with a period (.) or hyphen (-) character.
	//
	// +kubebuilder:validation:XValidation:rule="size(self.split('/')) == 11 && self.matches('^/subscriptions/.*/resourceGroups/.*/providers/Microsoft.Network/virtualNetworks/.*/subnets/.*$')",message="encryptionSetID must be in the format `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}`"
	// +kubeubilder:validation:XValidation:rule="self.split('/')[2].matches('^[{]?[0-9a-fA-F]{8}-([0-9a-fA-F]{4}-){3}[0-9a-fA-F]{12}[}]?$')",message="the subscriptionId in the encryptionSetID must be a valid UUID. It should be 5 groups of hyphen separated hexadecimal characters in the form 8-4-4-4-12"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')`,message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the subnetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[8].matches('[a-zA-Z0-9-_\\.]{2,64}')`,message="The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[8].endsWith('.') && !self.split('/')[8].endsWith('-')",message="the vnetName in the subnetID must not end with either a period (.) or hyphen (-) character"
	// +kubebuilder:validation:XValidation:rule=`self.split('/')[10].matches('[a-zA-Z0-9][a-zA-Z0-9-_\\.]{0,79}')`,message="The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[10].endsWith('.') && !self.split('/')[10].endsWith('-')",message="the subnetName in the subnetID must not end with a period (.) or hyphen (-) character"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
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
	// +openshift:enable:FeatureGate=AROHCPManagedIdentities
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
	// +openshift:enable:FeatureGate=AROHCPManagedIdentities
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
