package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	"github.com/blang/semver"
)

// dummySSHKey is a base64 encoded dummy SSH public key.
// The CAPI AzureMachineTemplate requires an SSH key to be set, so we provide a dummy one here.
const dummySSHKey = "c3NoLXJzYSBBQUFBQjNOemFDMXljMkVBQUFBREFRQUJBQUFCQVFDTGFjOTR4dUE4QjkyMEtjejhKNjhUdmZCRjQyR2UwUllXSUx3Lzd6dDhUQlU5ell5Q0Q2K0ZlekFwWndLRjB1V3luMGVBQmlBWVdIV0tKbENxS0VIT2hOQmV2Mkx3S0dnZHFqM0dvcHV2N3RpZFVqSVpqYi9DVWtjQVRZUWhMWkxVTCs3eWkzRThKNHdhYkxEMWVNS1p1U3ZmMUsxT0RwVUFXYTkwbWVmR0FBOVdIVEhMcnF1UUpWdC9JT0JKN1ROZFNwMDVuM0Ywa29xZlE2empwRlFYMk8zaWJUc29yR3ZEekdhYS9yUENxQWhTSjRJaEhnMDNVb3FBbVlraW51NTFvVEcxRlRXaTh2b00vRVJ4TlduamNUSElET1JmYmo2bFVyZ3Zkci9MZGtqc2dFcENiNEMxUS9IbW5MRHVpTEdPM2tNZ2cyOHFzZ0ZmTHloUjl3ay8K"

// azureMarketplaceMetadata represents the Azure Marketplace metadata from the release payload
// This matches the structure found in the release-manifests/0000_50_installer_coreos-bootimages.yaml
// under .data.stream.architectures.<arch>.rhel-coreos-extensions.marketplace.azure
type azureMarketplaceMetadata struct {
	NoPurchasePlan *azureMarketplaceImageInfo `json:"no-purchase-plan,omitempty"`
}

type azureMarketplaceImageInfo struct {
	HyperVGen1 *hyperv1.AzureMarketplaceImage `json:"hyperVGen1,omitempty"`
	HyperVGen2 *hyperv1.AzureMarketplaceImage `json:"hyperVGen2,omitempty"`
}

// defaultAzureNodePoolImage applies Azure Marketplace image defaults for OCP >= 4.20
// when Type is AzureMarketplace and azureMarketplace data is not provided and marketplace metadata is available in the release payload.
func defaultAzureNodePoolImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) error {
	// Skip if ImageID is explicitly set
	if nodePool.Spec.Platform.Azure.Image.ImageID != nil {
		return nil
	}

	// Skip if AzureMarketplace is explicitly set with populated fields
	// An empty struct (all fields are empty strings) means the CLI created it but expects defaulting
	if nodePool.Spec.Platform.Azure.Image.AzureMarketplace != nil {
		marketplace := nodePool.Spec.Platform.Azure.Image.AzureMarketplace
		if marketplace.Publisher != "" && marketplace.Offer != "" &&
			marketplace.SKU != "" && marketplace.Version != "" {
			// User explicitly provided marketplace data, don't override
			return nil
		}
	}

	// Check if OCP version >= 4.20 for marketplace defaulting
	releaseVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("failed to parse release version %s: %w", releaseImage.Version(), err)
	}

	minVersionForMarketplace := semver.MustParse("4.20.0")
	if releaseVersion.LT(minVersionForMarketplace) {
		// Skip marketplace defaulting for versions < 4.20
		return nil
	}

	// Get architecture for the nodepool
	arch := nodePool.Spec.Arch
	if arch == "" {
		arch = hyperv1.ArchitectureAMD64 // Default to amd64 if not specified
	}

	// Map hypershift architecture to RHCOS stream architecture
	streamArch := arch
	switch arch {
	case hyperv1.ArchitectureARM64:
		streamArch = "aarch64"
	case hyperv1.ArchitectureAMD64:
		streamArch = "x86_64"
	}

	// Extract marketplace metadata from release payload
	azureMarketplace, err := getAzureMarketplaceMetadata(releaseImage, streamArch)
	if err != nil {
		return fmt.Errorf("failed to get Azure Marketplace metadata: %w", err)
	}

	if azureMarketplace == nil || azureMarketplace.NoPurchasePlan == nil {
		// No marketplace metadata available, skip defaulting
		return nil
	}

	// Determine which Hyper-V generation to use
	generation := hyperv1.Gen2 // Default to Gen2
	if nodePool.Spec.Platform.Azure.Image.AzureMarketplace != nil &&
		nodePool.Spec.Platform.Azure.Image.AzureMarketplace.ImageGeneration != nil {
		generation = *nodePool.Spec.Platform.Azure.Image.AzureMarketplace.ImageGeneration
	}

	var marketplaceImage *hyperv1.AzureMarketplaceImage
	switch generation {
	case hyperv1.Gen1:
		marketplaceImage = azureMarketplace.NoPurchasePlan.HyperVGen1
	case hyperv1.Gen2:
		marketplaceImage = azureMarketplace.NoPurchasePlan.HyperVGen2
	default:
		return fmt.Errorf("unsupported image generation %q, must be Gen1 or Gen2", generation)
	}

	if marketplaceImage == nil {
		return fmt.Errorf("no Azure Marketplace image available for %s generation %s", streamArch, generation)
	}

	// Apply the marketplace image defaults
	nodePool.Spec.Platform.Azure.Image.Type = hyperv1.AzureMarketplace
	nodePool.Spec.Platform.Azure.Image.AzureMarketplace = marketplaceImage

	return nil
}

// getAzureMarketplaceMetadata extracts Azure Marketplace metadata from the release payload
func getAzureMarketplaceMetadata(releaseImage *releaseinfo.ReleaseImage, arch string) (*azureMarketplaceMetadata, error) {
	if releaseImage.StreamMetadata == nil {
		return nil, nil // No stream metadata available
	}

	archData, foundArch := releaseImage.StreamMetadata.Architectures[arch]
	if !foundArch {
		return nil, fmt.Errorf("architecture %s not found in stream metadata", arch)
	}

	// Extract marketplace metadata from the RHCOS extensions
	// Structure: .architectures.<arch>.rhel-coreos-extensions.marketplace.azure.no-purchase-plan
	// Check for nil safety before accessing nested fields
	if archData.RHCOS.Marketplace.Azure.NoPurchasePlan.HyperVGen1 == nil &&
		archData.RHCOS.Marketplace.Azure.NoPurchasePlan.HyperVGen2 == nil {
		return nil, nil // No marketplace data available
	}
	azureMarketplace := archData.RHCOS.Marketplace.Azure.NoPurchasePlan

	// Convert from release info format to our internal format
	result := &azureMarketplaceMetadata{
		NoPurchasePlan: &azureMarketplaceImageInfo{},
	}

	if azureMarketplace.HyperVGen1 != nil {
		result.NoPurchasePlan.HyperVGen1 = &hyperv1.AzureMarketplaceImage{
			Publisher: azureMarketplace.HyperVGen1.Publisher,
			Offer:     azureMarketplace.HyperVGen1.Offer,
			SKU:       azureMarketplace.HyperVGen1.SKU,
			Version:   azureMarketplace.HyperVGen1.Version,
		}
	}

	if azureMarketplace.HyperVGen2 != nil {
		result.NoPurchasePlan.HyperVGen2 = &hyperv1.AzureMarketplaceImage{
			Publisher: azureMarketplace.HyperVGen2.Publisher,
			Offer:     azureMarketplace.HyperVGen2.Offer,
			SKU:       azureMarketplace.HyperVGen2.SKU,
			Version:   azureMarketplace.HyperVGen2.Version,
		}
	}

	return result, nil
}

func azureMachineTemplateSpec(nodePool *hyperv1.NodePool) (*capiazure.AzureMachineTemplateSpec, error) {
	subnetName, err := azureutil.GetSubnetNameFromSubnetID(nodePool.Spec.Platform.Azure.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine subnet name for Azure machine: %w", err)
	}

	// Validate that either ImageID or AzureMarketplace is set after defaulting
	// For OCP >= 4.20, defaultAzureNodePoolImage should have populated marketplace image from release payload
	// For earlier versions or when marketplace metadata is unavailable, users must explicitly provide marketplace flags or upload a boot image
	if nodePool.Spec.Platform.Azure.Image.ImageID == nil && nodePool.Spec.Platform.Azure.Image.AzureMarketplace == nil {
		return nil, fmt.Errorf("no Azure VM image configured: either provide marketplace flags (--marketplace-publisher, etc.) or ensure the release image contains marketplace metadata (available in OCP 4.20+)")
	}

	azureMachineTemplate := &capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{
		VMSize: nodePool.Spec.Platform.Azure.VMSize,
		OSDisk: capiazure.OSDisk{
			DiskSizeGB: ptr.To(nodePool.Spec.Platform.Azure.OSDisk.SizeGiB),
			ManagedDisk: &capiazure.ManagedDiskParameters{
				StorageAccountType: string(nodePool.Spec.Platform.Azure.OSDisk.DiskStorageAccountType),
			},
		},
		NetworkInterfaces: []capiazure.NetworkInterface{{
			SubnetName: subnetName,
		}},
		FailureDomain: failureDomain(nodePool),
	}}}

	switch nodePool.Spec.Platform.Azure.Image.Type {
	case hyperv1.ImageID:
		azureMachineTemplate.Template.Spec.Image = &capiazure.Image{
			ID: nodePool.Spec.Platform.Azure.Image.ImageID,
		}
	case hyperv1.AzureMarketplace:
		azureMachineTemplate.Template.Spec.Image = &capiazure.Image{
			Marketplace: &capiazure.AzureMarketplaceImage{
				ImagePlan: capiazure.ImagePlan{
					Publisher: nodePool.Spec.Platform.Azure.Image.AzureMarketplace.Publisher,
					Offer:     nodePool.Spec.Platform.Azure.Image.AzureMarketplace.Offer,
					SKU:       nodePool.Spec.Platform.Azure.Image.AzureMarketplace.SKU,
				},
				Version: nodePool.Spec.Platform.Azure.Image.AzureMarketplace.Version,
			},
		}
	}

	if nodePool.Spec.Platform.Azure.OSDisk.EncryptionSetID != "" {
		azureMachineTemplate.Template.Spec.OSDisk.ManagedDisk.DiskEncryptionSet = &capiazure.DiskEncryptionSetParameters{
			ID: nodePool.Spec.Platform.Azure.OSDisk.EncryptionSetID,
		}
	}

	if nodePool.Spec.Platform.Azure.EncryptionAtHost == "Enabled" {
		azureMachineTemplate.Template.Spec.SecurityProfile = &capiazure.SecurityProfile{
			EncryptionAtHost: to.Ptr(true),
		}
	}

	if nodePool.Spec.Platform.Azure.OSDisk.Persistence == hyperv1.EphemeralDiskPersistence {
		// This is set to "None" if not explicitly set - https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/f44d953844de58e4b6fe8f51d88b0bf75a04e9ec/api/v1beta1/azuremachine_default.go#L54
		// "VMs and VM Scale Set Instances using an ephemeral OS disk support only Readonly caching."
		azureMachineTemplate.Template.Spec.OSDisk.CachingType = "ReadOnly"
		azureMachineTemplate.Template.Spec.OSDisk.DiffDiskSettings = &capiazure.DiffDiskSettings{Option: "Local"}
	}

	if nodePool.Spec.Platform.Azure.Diagnostics != nil && nodePool.Spec.Platform.Azure.Diagnostics.StorageAccountType != "" {
		azureMachineTemplate.Template.Spec.Diagnostics = &capiazure.Diagnostics{
			Boot: &capiazure.BootDiagnostics{
				StorageAccountType: capiazure.BootDiagnosticsStorageAccountType(nodePool.Spec.Platform.Azure.Diagnostics.StorageAccountType),
			},
		}
		if nodePool.Spec.Platform.Azure.Diagnostics.StorageAccountType == "UserManaged" {
			azureMachineTemplate.Template.Spec.Diagnostics.Boot.UserManaged = &capiazure.UserManagedBootDiagnostics{
				StorageAccountURI: nodePool.Spec.Platform.Azure.Diagnostics.UserManaged.StorageAccountURI,
			}
		}
	}

	azureMachineTemplate.Template.Spec.SSHPublicKey = dummySSHKey

	return azureMachineTemplate, nil
}

func (c *CAPI) azureMachineTemplate(ctx context.Context, templateNameGenerator func(spec any) (string, error)) (*capiazure.AzureMachineTemplate, error) {
	// Apply Azure Marketplace image defaults before generating machine template spec
	if err := defaultAzureNodePoolImage(c.nodePool, c.ConfigGenerator.rolloutConfig.releaseImage); err != nil {
		return nil, fmt.Errorf("failed to apply Azure image defaults: %w", err)
	}

	spec, err := azureMachineTemplateSpec(c.nodePool)
	if err != nil {
		return nil, fmt.Errorf("failed to generate AzureMachineTemplateSpec: %w", err)
	}

	templateName, err := templateNameGenerator(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate template name: %w", err)
	}

	template := &capiazure.AzureMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: templateName,
		},
		Spec: *spec,
	}

	return template, nil
}

func failureDomain(nodepool *hyperv1.NodePool) *string {
	if nodepool.Spec.Platform.Azure.AvailabilityZone == "" {
		return nil
	}
	return ptr.To(fmt.Sprintf("%v", nodepool.Spec.Platform.Azure.AvailabilityZone))
}
