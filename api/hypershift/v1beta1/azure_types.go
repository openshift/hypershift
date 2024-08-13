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
// +kubebuilder:validation:XValidation:rule="!has(self.diskStorageAccountType) || self.diskStorageAccountType != 'UltraSSD' || self.diskStorageAccountType <= 32,767",message=""
type AzureNodePoolPlatform struct {
	// vmSize is the Azure VM instance type to use for the nodes being created in the nodepool.
	//
	// TODO: This should be validated with a regex, based on https://learn.microsoft.com/en-us/azure/virtual-machines/vm-naming-conventions
	// +kubebuilder:validation:Required
	VMSize string `json:"vmSize"`

	// image is used to configure the VM boot image. If unset, the default image at the location below will be used and
	// is expected to exist: subscription/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Compute/images/rhcos.x86_64.vhd.
	// The <subscriptionID> and the <resourceGroupName> are expected to be the same resource group documented in the
	// Hosted Cluster specification respectively, HostedCluster.Spec.Platform.Azure.SubscriptionID and
	// HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// +kubebuilder:validation:Required
	Image AzureVMImage `json:"image"`

	// diskSizeGiB is the size in GiB (1024^3 bytes) to assign to the OS disk. This should be between 16 and 65,536.
	// When not set, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is 30.
	//
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=65536
	// +optional
	DiskSizeGB int32 `json:"diskSizeGB,omitempty"`

	// diskStorageAccountType is the disk storage account type to use.
	// Valid values are Standard, StandardSSD, PremiumSSD and UltraSSD and omitted.
	// Note that Standard means a HDD.
	// The disk performance is tied to the disk type, please refer to the Azure documentation for further details
	// https://docs.microsoft.com/en-us/azure/virtual-machines/disks-types#disk-type-comparison.
	// When omitted this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default is PremiumSSD.
	//
	// +kubebuilder:validation:Enum=Standard;StandardSSD;PremiumSSD;UltraSSD
	// +optional
	// TODO: Should all disk options come under a struct rather than be top level like this?
	DiskStorageAccountType string `json:"diskStorageAccountType,omitempty"`

	// availabilityZone is the failure domain identifier where the VM should be attached to.
	// This must not be specified for clusters in a location that does not support AvailabilityZone because... TODO: why?
	// Example values are TODO
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
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

	// diskEncryptionSetID is the ID of the DiskEncryptionSet resource to use to encrypt the OS disks for the VMs. This
	// needs to exist in the same subscription id listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// DiskEncryptionSetID should also exist in a resource group under the same subscription id and the same location
	// listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.Location.
	// TODO: What does this do for a customer, why would they want to set it?
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
	// +optional
	DiskEncryptionSetID string `json:"diskEncryptionSetID,omitempty"`

	// TODO: This shouldn't be a bool, but I don't know what the field is really for.
	// Should it be coming under the disk parameters? We need to know what the impact of this is for users.
	// Is this a particularly bad idea for control plane nodes, and does that matter for HCP, probably not.
	//
	// enableEphemeralOSDisk is a flag when set to true, will enable ephemeral OS disk.
	//
	// +optional
	EnableEphemeralOSDisk bool `json:"enableEphemeralOSDisk,omitempty"`

	// subnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same network, HostedCluster.Spec.Platform.Azure.VnetID, and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// subnetID is immutable once set.
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths?
	//
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
	// ImageID means ... TODO
	// AzureMarketplace means ... TODO
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
	// TODO: What is the valid character set for this field? What about minimum and maximum lengths
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Offer string `json:"offer"`

	// sku specifies an instance of an offer, such as a major release of a distribution.
	// For example, 18.04-LTS, 2019-Datacenter.
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
