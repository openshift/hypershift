package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestAzureMachineTemplateSpec(t *testing.T) {
	testCases := []struct {
		name                             string
		nodePool                         *hyperv1.NodePool
		expectedAzureMachineTemplateSpec *capiazure.AzureMachineTemplateSpec
		expectedErr                      bool
		expectedErrMsg                   string
	}{
		{
			name: "nominal case without managed identity",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("testImageID"),
							},
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1.ObjectMeta{Labels: nil, Annotations: nil},
					Spec: capiazure.AzureMachineSpec{
						ProviderID:    nil,
						VMSize:        "Standard_D2_v2",
						FailureDomain: nil,
						Image: &capiazure.Image{
							ID:             ptr.To("testImageID"),
							SharedGallery:  nil,
							Marketplace:    nil,
							ComputeGallery: nil,
						},
						UserAssignedIdentities:     nil,
						SystemAssignedIdentityRole: nil,
						RoleAssignmentName:         "",
						OSDisk: capiazure.OSDisk{
							OSType:     "",
							DiskSizeGB: ptr.To[int32](30),
							ManagedDisk: &capiazure.ManagedDiskParameters{
								StorageAccountType: "Standard_LRS",
								DiskEncryptionSet:  nil,
								SecurityProfile:    nil,
							},
							DiffDiskSettings: nil,
							CachingType:      "",
						},
						DataDisks:              nil,
						SSHPublicKey:           dummySSHKey,
						AdditionalTags:         nil,
						AdditionalCapabilities: nil,
						AllocatePublicIP:       false,
						EnableIPForwarding:     false,
						AcceleratedNetworking:  nil,
						Diagnostics:            nil,
						SpotVMOptions:          nil,
						SecurityProfile:        nil,
						SubnetName:             "",
						DNSServers:             nil,
						VMExtensions:           nil,
						NetworkInterfaces: []capiazure.NetworkInterface{
							{
								SubnetName:            "testSubnetName",
								PrivateIPConfigs:      0,
								AcceleratedNetworking: nil,
							},
						},
						CapacityReservationGroupID: nil,
					},
				},
			},
			expectedErr: false,
		},
		{
			name: "nominal case with managed identity and image ID",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("testImageID"),
							},
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1.ObjectMeta{Labels: nil, Annotations: nil},
					Spec: capiazure.AzureMachineSpec{
						ProviderID:    nil,
						VMSize:        "Standard_D2_v2",
						FailureDomain: nil,
						Image: &capiazure.Image{
							ID:             ptr.To("testImageID"),
							SharedGallery:  nil,
							Marketplace:    nil,
							ComputeGallery: nil,
						},
						Identity:                   "",
						SystemAssignedIdentityRole: nil,
						RoleAssignmentName:         "",
						OSDisk: capiazure.OSDisk{
							OSType:     "",
							DiskSizeGB: ptr.To[int32](30),
							ManagedDisk: &capiazure.ManagedDiskParameters{
								StorageAccountType: "Standard_LRS",
								DiskEncryptionSet:  nil,
								SecurityProfile:    nil,
							},
							DiffDiskSettings: nil,
							CachingType:      "",
						},
						DataDisks:              nil,
						SSHPublicKey:           dummySSHKey,
						AdditionalTags:         nil,
						AdditionalCapabilities: nil,
						AllocatePublicIP:       false,
						EnableIPForwarding:     false,
						AcceleratedNetworking:  nil,
						Diagnostics:            nil,
						SpotVMOptions:          nil,
						SecurityProfile:        nil,
						SubnetName:             "",
						DNSServers:             nil,
						VMExtensions:           nil,
						NetworkInterfaces: []capiazure.NetworkInterface{
							{
								SubnetName:            "testSubnetName",
								PrivateIPConfigs:      0,
								AcceleratedNetworking: nil,
							},
						},
						CapacityReservationGroupID: nil,
					},
				},
			},
			expectedErr: false,
		},
		{
			name: "nominal case with managed identity and marketplace image",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1.ObjectMeta{Labels: nil, Annotations: nil},
					Spec: capiazure.AzureMachineSpec{
						ProviderID:    nil,
						VMSize:        "Standard_D2_v2",
						FailureDomain: nil,
						Image: &capiazure.Image{
							Marketplace: &capiazure.AzureMarketplaceImage{
								ImagePlan: capiazure.ImagePlan{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
								},
								Version:         "testVersion",
								ThirdPartyImage: false,
							},
						},
						Identity:                   "",
						SystemAssignedIdentityRole: nil,
						RoleAssignmentName:         "",
						OSDisk: capiazure.OSDisk{
							OSType:     "",
							DiskSizeGB: ptr.To[int32](30),
							ManagedDisk: &capiazure.ManagedDiskParameters{
								StorageAccountType: "Standard_LRS",
								DiskEncryptionSet:  nil,
								SecurityProfile:    nil,
							},
							DiffDiskSettings: nil,
							CachingType:      "",
						},
						DataDisks:              nil,
						SSHPublicKey:           dummySSHKey,
						AdditionalTags:         nil,
						AdditionalCapabilities: nil,
						AllocatePublicIP:       false,
						EnableIPForwarding:     false,
						AcceleratedNetworking:  nil,
						Diagnostics:            nil,
						SpotVMOptions:          nil,
						SecurityProfile:        nil,
						SubnetName:             "",
						DNSServers:             nil,
						VMExtensions:           nil,
						NetworkInterfaces: []capiazure.NetworkInterface{
							{
								SubnetName:            "testSubnetName",
								PrivateIPConfigs:      0,
								AcceleratedNetworking: nil,
							},
						},
						CapacityReservationGroupID: nil,
					},
				},
			},
			expectedErr: false,
		},
		{
			name: "nominal case with managed identity, AvailabilityZone, AzureMarketplace, DiskEncryptionSetID, EnableEphemeralOSDisk, Diagnostics, and managed StorageAccountType",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							AvailabilityZone: "1",
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								EncryptionSetID:        "testDES_ID",
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
								Persistence:            hyperv1.EphemeralDiskPersistence,
							},
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "Managed",
							},
						},
					},
				},
			},
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1.ObjectMeta{Labels: nil, Annotations: nil},
					Spec: capiazure.AzureMachineSpec{
						ProviderID:    nil,
						VMSize:        "Standard_D2_v2",
						FailureDomain: ptr.To("1"),
						Image: &capiazure.Image{
							Marketplace: &capiazure.AzureMarketplaceImage{
								ImagePlan: capiazure.ImagePlan{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
								},
								Version:         "testVersion",
								ThirdPartyImage: false,
							},
						},
						Identity:                   "",
						SystemAssignedIdentityRole: nil,
						RoleAssignmentName:         "",
						OSDisk: capiazure.OSDisk{
							OSType:     "",
							DiskSizeGB: ptr.To[int32](30),
							ManagedDisk: &capiazure.ManagedDiskParameters{
								StorageAccountType: "Standard_LRS",
								DiskEncryptionSet:  &capiazure.DiskEncryptionSetParameters{ID: "testDES_ID"},
								SecurityProfile:    nil,
							},
							DiffDiskSettings: &capiazure.DiffDiskSettings{Option: "Local"},
							CachingType:      "ReadOnly",
						},
						DataDisks:              nil,
						SSHPublicKey:           dummySSHKey,
						AdditionalTags:         nil,
						AdditionalCapabilities: nil,
						AllocatePublicIP:       false,
						EnableIPForwarding:     false,
						AcceleratedNetworking:  nil,
						Diagnostics: &capiazure.Diagnostics{
							Boot: &capiazure.BootDiagnostics{
								StorageAccountType: "Managed",
								UserManaged:        nil,
							},
						},
						SpotVMOptions: nil,
						SubnetName:    "",
						DNSServers:    nil,
						VMExtensions:  nil,
						NetworkInterfaces: []capiazure.NetworkInterface{
							{
								SubnetName:            "testSubnetName",
								PrivateIPConfigs:      0,
								AcceleratedNetworking: nil,
							},
						},
						CapacityReservationGroupID: nil,
					},
				},
			},
			expectedErr: false,
		},
		{
			name: "nominal case with managed identity, AzureMarketplace, DiskEncryptionSetID, EnableEphemeralOSDisk, Diagnostics, and user-managed StorageAccountType",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								EncryptionSetID:        "testDES_ID",
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
								Persistence:            hyperv1.EphemeralDiskPersistence,
							},
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								UserManaged: &hyperv1.UserManagedDiagnostics{
									StorageAccountURI: "www.test.com",
								},
							},
						},
					},
				},
			},
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1.ObjectMeta{Labels: nil, Annotations: nil},
					Spec: capiazure.AzureMachineSpec{
						ProviderID:    nil,
						VMSize:        "Standard_D2_v2",
						FailureDomain: nil,
						Image: &capiazure.Image{
							Marketplace: &capiazure.AzureMarketplaceImage{
								ImagePlan: capiazure.ImagePlan{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
								},
								Version:         "testVersion",
								ThirdPartyImage: false,
							},
						},
						Identity:                   "",
						SystemAssignedIdentityRole: nil,
						RoleAssignmentName:         "",
						OSDisk: capiazure.OSDisk{
							OSType:     "",
							DiskSizeGB: ptr.To[int32](30),
							ManagedDisk: &capiazure.ManagedDiskParameters{
								StorageAccountType: "Standard_LRS",
								DiskEncryptionSet:  &capiazure.DiskEncryptionSetParameters{ID: "testDES_ID"},
								SecurityProfile:    nil,
							},
							DiffDiskSettings: &capiazure.DiffDiskSettings{Option: "Local"},
							CachingType:      "ReadOnly",
						},
						DataDisks:              nil,
						SSHPublicKey:           dummySSHKey,
						AdditionalTags:         nil,
						AdditionalCapabilities: nil,
						AllocatePublicIP:       false,
						EnableIPForwarding:     false,
						AcceleratedNetworking:  nil,
						Diagnostics: &capiazure.Diagnostics{
							Boot: &capiazure.BootDiagnostics{
								StorageAccountType: "UserManaged",
								UserManaged: &capiazure.UserManagedBootDiagnostics{
									StorageAccountURI: "www.test.com",
								},
							},
						},
						SpotVMOptions: nil,
						SubnetName:    "",
						DNSServers:    nil,
						VMExtensions:  nil,
						NetworkInterfaces: []capiazure.NetworkInterface{
							{
								SubnetName:            "testSubnetName",
								PrivateIPConfigs:      0,
								AcceleratedNetworking: nil,
							},
						},
						CapacityReservationGroupID: nil,
					},
				},
			},
			expectedErr: false,
		},
		{
			name: "error case since ImageID and AzureMarketplace are not provided",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:   "Standard_D2_v2",

							OSDisk: hyperv1.AzureNodePoolOSDisk{
								EncryptionSetID:        "testDES_ID",
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
								Persistence:            hyperv1.EphemeralDiskPersistence,
							},
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								UserManaged: &hyperv1.UserManagedDiagnostics{
									StorageAccountURI: "www.test.com",
								},
							},
						},
					},
				},
			},
			expectedErr:    true,
			expectedErrMsg: "either ImageID or AzureMarketplace needs to be provided for the Azure machine",
		},
		{
			name: "error case since a bad subnetID was provided",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							SubnetID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/",
							VMSize:   "Standard_D2_v2",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								EncryptionSetID:        "testDES_ID",
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
								Persistence:            hyperv1.EphemeralDiskPersistence,
							},
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								UserManaged: &hyperv1.UserManagedDiagnostics{
									StorageAccountURI: "www.test.com",
								},
							},
						},
					},
				},
			},
			expectedErr:    true,
			expectedErrMsg: "failed to determine subnet name for Azure machine: failed to parse subnet name from \"/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/\"",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			azureSpec, err := azureMachineTemplateSpec(tc.nodePool)
			if tc.expectedErr {
				g.Expect(err.Error()).To(Equal(tc.expectedErrMsg))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(azureSpec).To(Equal(tc.expectedAzureMachineTemplateSpec))
			}
		})
	}
}

func TestDefaultAzureNodePoolImage(t *testing.T) {
	testCases := []struct {
		name                     string
		nodePool                 *hyperv1.NodePool
		releaseImage             *releaseinfo.ReleaseImage
		expectedImageType        hyperv1.AzureVMImageType
		expectedMarketplaceImage *hyperv1.AzureMarketplaceImage
		expectedError            bool
		expectedErrorMsg         string
	}{
		{
			name: "skip defaulting when image is already set - ImageID",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("existing-image-id"),
							},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.20.0", true),
			expectedImageType:        hyperv1.ImageID,
			expectedMarketplaceImage: nil,
		},
		{
			name: "skip defaulting when AzureMarketplace is already set",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									Publisher: "existing-publisher",
									Offer:     "existing-offer",
									SKU:       "existing-sku",
									Version:   "existing-version",
								},
							},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher: "existing-publisher",
				Offer:     "existing-offer",
				SKU:       "existing-sku",
				Version:   "existing-version",
			},
		},
		{
			name: "skip defaulting for OCP < 4.20",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.19.5", true),
			expectedImageType:        "",
			expectedMarketplaceImage: nil,
		},
		{
			name: "skip defaulting when no marketplace metadata for OCP >= 4.20 (current behavior)",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.20.0", true),
			expectedImageType:        "",
			expectedMarketplaceImage: nil,
		},
		{
			name: "skip defaulting with Gen1 imageGeneration when no marketplace metadata",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								ImageGeneration: ptr.To(hyperv1.Gen1),
							},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.20.0", true),
			expectedImageType:        "",
			expectedMarketplaceImage: nil,
		},
		{
			name: "skip defaulting with Gen2 imageGeneration when no marketplace metadata",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								ImageGeneration: ptr.To(hyperv1.Gen2),
							},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.20.0", true),
			expectedImageType:        "",
			expectedMarketplaceImage: nil,
		},
		{
			name: "skip defaulting when no marketplace metadata available",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{},
						},
					},
				},
			},
			releaseImage:             createMockReleaseImage("4.20.0", false),
			expectedImageType:        "",
			expectedMarketplaceImage: nil,
		},
		{
			name: "error with unsupported imageGeneration when marketplace metadata is available",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								ImageGeneration: ptr.To(hyperv1.AzureVMImageGeneration("Gen3")),
							},
						},
					},
				},
			},
			releaseImage:     createMockReleaseImage("4.20.0", true),
			expectedError:    true,
			expectedErrorMsg: "unsupported image generation \"Gen3\", must be Gen1 or Gen2",
		},
		{
			name: "apply marketplace defaults for Gen2 when metadata is available",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher: "azureopenshift",
				Offer:     "aro4",
				SKU:       "419-v2",
				Version:   "419.6.20250523",
			},
		},
		{
			name: "apply marketplace defaults for Gen1 when specified and metadata is available",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								ImageGeneration: ptr.To(hyperv1.Gen1),
							},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher: "azureopenshift",
				Offer:     "aro4",
				SKU:       "aro_419",
				Version:   "419.6.20250523",
			},
		},
		{
			name: "apply marketplace defaults for ARM64 Gen2",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureARM64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher: "azureopenshift",
				Offer:     "aro4",
				SKU:       "419-v2",
				Version:   "419.6.20250523",
			},
		},
		{
			name: "apply marketplace defaults for ARM64 Gen1",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureARM64,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								ImageGeneration: ptr.To(hyperv1.Gen1),
							},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher: "azureopenshift",
				Offer:     "aro4",
				SKU:       "aro_419",
				Version:   "419.6.20250523",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := defaultAzureNodePoolImage(context.Background(), tc.nodePool, tc.releaseImage)

			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tc.expectedImageType != "" {
					g.Expect(tc.nodePool.Spec.Platform.Azure.Image.Type).To(Equal(tc.expectedImageType))
				}
				if tc.expectedMarketplaceImage != nil {
					g.Expect(tc.nodePool.Spec.Platform.Azure.Image.AzureMarketplace).To(Equal(tc.expectedMarketplaceImage))
				}
			}
		})
	}
}

// createMockReleaseImage creates a mock release image for testing
func createMockReleaseImage(version string, hasMarketplaceMetadata bool) *releaseinfo.ReleaseImage {
	architecture := releaseinfo.CoreOSArchitecture{
		Artifacts: map[string]releaseinfo.CoreOSArtifact{},
		Images:    releaseinfo.CoreOSImages{},
		RHCOS: releaseinfo.CoreRHCOSImage{
			AzureDisk: releaseinfo.CoreAzureDisk{
				Release: "9.6.20250701-0",
				URL:     "https://rhcos.blob.core.windows.net/imagebucket/rhcos-9.6.20250701-0-azure.x86_64.vhd",
			},
		},
	}

	if hasMarketplaceMetadata {
		architecture.RHCOS.Marketplace = releaseinfo.CoreMarketplace{
			Azure: releaseinfo.CoreAzureMarketplace{
				NoPurchasePlan: releaseinfo.CoreAzureMarketplaceNoPurchasePlan{
					HyperVGen1: &releaseinfo.CoreAzureMarketplaceImage{
						Publisher: "azureopenshift",
						Offer:     "aro4",
						SKU:       "aro_419",
						Version:   "419.6.20250523",
					},
					HyperVGen2: &releaseinfo.CoreAzureMarketplaceImage{
						Publisher: "azureopenshift",
						Offer:     "aro4",
						SKU:       "419-v2",
						Version:   "419.6.20250523",
					},
				},
			},
		}
	}

	architectures := map[string]releaseinfo.CoreOSArchitecture{
		"x86_64": architecture,
	}

	streamMetadata := &releaseinfo.CoreOSStreamMetadata{
		Stream:        "test-stream",
		Architectures: architectures,
	}

	// Create a simple ImageStream for the version
	// The Version() method returns ImageStream.Name, so we set that to the version
	imageStream := &imageapi.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name: version,
		},
		Status: imageapi.ImageStreamStatus{
			Tags: []imageapi.NamedTagEventList{
				{
					Tag: version,
					Items: []imageapi.TagEvent{
						{},
					},
				},
			},
		},
	}

	return &releaseinfo.ReleaseImage{
		ImageStream:    imageStream,
		StreamMetadata: streamMetadata,
	}
}
