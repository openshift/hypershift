package v1beta1

import "fmt"

type AzureVMImageType string

const (
	ImageID          AzureVMImageType = "ImageID"
	AzureMarketplace AzureVMImageType = "AzureMarketplace"
)

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
