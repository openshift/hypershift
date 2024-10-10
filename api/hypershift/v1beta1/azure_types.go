package v1beta1

import "fmt"

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

	// availabilityZone is the failure domain identifier where the VM should be attached to.
	// This must not be specified for clusters in a location that does not support AvailabilityZone because... TODO: why?
	// Availability zones are identified by numbers, either 1, 2 or 3.
	//
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	// +optional
	AvailabilityZone int32 `json:"availabilityZone,omitempty"`

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
	// +kubebuilder:validation:XValidation:rule="self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')",message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[4].endsWith('.')",message="the resourceGroupName in the subnetID must not end with a period (.) character"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[8].matches('[a-zA-Z0-9-_\\.]{2,64}')",message="The vnetName should be between 2 and 64 characters, consisting only of alphanumeric characters, hyphens, underscores and periods"
	// +kubebuilder:validation:XValidation:rule="!self.split('/')[8].endsWith('.') && !self.split('/')[8].endsWith('-')",message="the vnetName in the subnetID must not end with either a period (.) or hyphen (-) character"
	// +kubebuilder:validation:XValidation:rule="self.split('/')[10].matches('[a-zA-Z0-9][a-zA-Z0-9-_\\.]{0,79}')",message="The subnetName should be between 1 and 80 characters, consisting only of alphanumeric characters, hyphens and underscores and must start with an alphanumeric character"
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
	Type AzureVMImageType `json:"azureImageType"`

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
	AzureMarketplace *MarketplaceImage `json:"azureMarketplace,omitempty"`
}

// MarketplaceImage specifies the information needed to create an Azure VM from an Azure Marketplace image.
// + This struct replicates the same fields found in CAPZ - https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/main/api/v1beta1/types.go.
type MarketplaceImage struct {
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
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).scheme() == 'https'", message="storageAccountURI must be a valid HTTPS URL"
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

// +kubebuilder:validation:XValidation:rule="!has(self.storageAccountType) || self.storageAccountType != 'UltraSSD' || self.sizeGiB <= 32,767",message="When not using storageAccountType UltraSSD, the sizeGiB value must be less than or equal to 32,767"
type AzureNodePoolOSDisk struct {
	// sizeGiB is the size in GiB (1024^3 bytes) to assign to the OS disk.
	// This should be between 16 and 65,536 when using the UltraSSD storage account type and between 16 and 32,767 when using any other storage account type.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is 30.
	//
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=65536
	// +optional
	SizeGB int32 `json:"sizeGB,omitempty"`

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
	// +kubebuilder:validation:XValidation:rule="self.split('/')[4].matches('[a-zA-Z0-9-_\\(\\)\\.]{1,90}')",message="The resourceGroupName should be between 1 and 90 characters, consisting only of alphanumeric characters, hyphens, underscores, periods and paranthesis"
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
