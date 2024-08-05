package nodepool

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"golang.org/x/crypto/ssh"

	"k8s.io/utils/ptr"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
)

func azureMachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, existing capiazure.AzureMachineTemplateSpec) (*capiazure.AzureMachineTemplateSpec, error) {
	// The azure api requires passing a public key. This key is randomly generated, the private portion is thrown away and the public key
	// gets written to the template.
	sshKey := existing.Template.Spec.SSHPublicKey
	if sshKey == "" {
		var err error
		sshKey, err = generateSSHPubkey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate a SSH key: %w", err)
		}
	}

	subnetName, err := azureutil.GetSubnetNameFromSubnetID(nodePool.Spec.Platform.Azure.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine subnet name for Azure machine: %w", err)
	}

	// This should never happen by design with the CEL validation on nodePool.Spec.Platform.Azure.Image
	if nodePool.Spec.Platform.Azure.Image.ImageID == nil && nodePool.Spec.Platform.Azure.Image.AzureMarketplace == nil {
		return nil, fmt.Errorf("either ImageID or AzureMarketplace needs to be provided for the Azure machine")
	}

	azureMachineTemplate := &capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{
		VMSize: nodePool.Spec.Platform.Azure.VMSize,
		OSDisk: capiazure.OSDisk{
			DiskSizeGB: ptr.To(nodePool.Spec.Platform.Azure.DiskSizeGB),
			ManagedDisk: &capiazure.ManagedDiskParameters{
				StorageAccountType: nodePool.Spec.Platform.Azure.DiskStorageAccountType,
			},
		},
		NetworkInterfaces: []capiazure.NetworkInterface{{
			SubnetName: subnetName,
		}},
		SSHPublicKey:  sshKey,
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

	if nodePool.Spec.Platform.Azure.MachineIdentityID != "" {
		azureMachineTemplate.Template.Spec.Identity = capiazure.VMIdentityUserAssigned
		azureMachineTemplate.Template.Spec.UserAssignedIdentities = []capiazure.UserAssignedIdentity{{
			ProviderID: nodePool.Spec.Platform.Azure.MachineIdentityID,
		}}
	}

	if nodePool.Spec.Platform.Azure.DiskEncryptionSetID != "" {
		azureMachineTemplate.Template.Spec.OSDisk.ManagedDisk.DiskEncryptionSet = &capiazure.DiskEncryptionSetParameters{
			ID: nodePool.Spec.Platform.Azure.DiskEncryptionSetID,
		}
		azureMachineTemplate.Template.Spec.SecurityProfile = &capiazure.SecurityProfile{
			EncryptionAtHost: to.Ptr(true),
		}
	}

	if nodePool.Spec.Platform.Azure.EnableEphemeralOSDisk {
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
				StorageAccountURI: nodePool.Spec.Platform.Azure.Diagnostics.StorageAccountURI,
			}
		}
	}

	return azureMachineTemplate, nil
}

func generateSSHPubkey() (string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}

	publicRsaKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to generate public key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(ssh.MarshalAuthorizedKey(publicRsaKey)), nil
}

func failureDomain(nodepool *hyperv1.NodePool) *string {
	if nodepool.Spec.Platform.Azure.AvailabilityZone == "" {
		return nil
	}
	return ptr.To(nodepool.Spec.Platform.Azure.AvailabilityZone)
}
