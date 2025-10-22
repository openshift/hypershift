package nodepool

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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

func TestAzureMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                   string
		nodePool               *hyperv1.NodePool
		templateNameGenerator  func(spec any) (string, error)
		expectedTemplateName   string
		expectedErr            bool
		expectedErrMsg         string
		validateTemplateSpec   bool
		expectedVMSize         string
		expectedSubnetName     string
		expectedImageID        *string
		expectedMarketplace    *capiazure.AzureMarketplaceImage
		expectedDiskSizeGB     *int32
		expectedStorageAccount string
	}{
		{
			name: "When NodePool has valid ImageID, it should create Azure machine template successfully",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("test-image-id"),
							},
							SubnetID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/subnet-worker",
							VMSize:   "Standard_D4s_v3",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                120,
								DiskStorageAccountType: "Premium_LRS",
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "azure-machine-template-test", nil
			},
			expectedTemplateName:   "azure-machine-template-test",
			expectedErr:            false,
			validateTemplateSpec:   true,
			expectedVMSize:         "Standard_D4s_v3",
			expectedSubnetName:     "subnet-worker",
			expectedImageID:        ptr.To("test-image-id"),
			expectedDiskSizeGB:     ptr.To[int32](120),
			expectedStorageAccount: "Premium_LRS",
		},
		{
			name: "When NodePool has AzureMarketplace image, it should create template with marketplace configuration",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									Publisher: "RedHat",
									Offer:     "RHEL",
									SKU:       "8-lvm-gen2",
									Version:   "latest",
								},
							},
							SubnetID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/subnet-worker",
							VMSize:   "Standard_D2s_v3",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                64,
								DiskStorageAccountType: "StandardSSD_LRS",
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "azure-marketplace-template", nil
			},
			expectedTemplateName: "azure-marketplace-template",
			expectedErr:          false,
			validateTemplateSpec: true,
			expectedVMSize:       "Standard_D2s_v3",
			expectedSubnetName:   "subnet-worker",
			expectedMarketplace: &capiazure.AzureMarketplaceImage{
				ImagePlan: capiazure.ImagePlan{
					Publisher: "RedHat",
					Offer:     "RHEL",
					SKU:       "8-lvm-gen2",
				},
				Version:         "latest",
				ThirdPartyImage: false,
			},
			expectedDiskSizeGB:     ptr.To[int32](64),
			expectedStorageAccount: "StandardSSD_LRS",
		},
		{
			name: "When subnet ID is invalid, it should return error from spec generation",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("test-image"),
							},
							SubnetID: "invalid-subnet-id",
							VMSize:   "Standard_D2s_v3",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "should-not-be-called", nil
			},
			expectedErr:    true,
			expectedErrMsg: "failed to generate AzureMachineTemplateSpec: failed to determine subnet name for Azure machine",
		},
		{
			name: "When template name generator fails, it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("test-image"),
							},
							SubnetID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/subnet-worker",
							VMSize:   "Standard_D2s_v3",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "", fmt.Errorf("template name generation failed")
			},
			expectedErr:    true,
			expectedErrMsg: "failed to generate template name: template name generation failed",
		},
		{
			name: "When NodePool has no image configured, it should return error",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.ImageID,
								// ImageID intentionally nil
							},
							SubnetID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/subnet-worker",
							VMSize:   "Standard_D2s_v3",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "should-not-be-called", nil
			},
			expectedErr:    true,
			expectedErrMsg: "failed to generate AzureMachineTemplateSpec: either ImageID or AzureMarketplace needs to be provided for the Azure machine",
		},
		{
			name: "When NodePool has encryption and ephemeral disk, it should create template with security configuration",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("test-image"),
							},
							SubnetID:         "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/subnet-worker",
							VMSize:           "Standard_D2s_v3",
							EncryptionAtHost: "Enabled",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Premium_LRS",
								EncryptionSetID:        "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Compute/diskEncryptionSets/des-test",
								Persistence:            hyperv1.EphemeralDiskPersistence,
							},
						},
					},
				},
			},
			templateNameGenerator: func(spec any) (string, error) {
				return "azure-secure-template", nil
			},
			expectedTemplateName: "azure-secure-template",
			expectedErr:          false,
			validateTemplateSpec: true,
			expectedVMSize:       "Standard_D2s_v3",
			expectedSubnetName:   "subnet-worker",
			expectedImageID:      ptr.To("test-image"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create a CAPI instance with minimal required fields
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool: tc.nodePool,
					},
				},
			}

			// Call the method under test
			template, err := capi.azureMachineTemplate(tc.templateNameGenerator)

			if tc.expectedErr {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
				g.Expect(template).To(BeNil())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(template).ToNot(BeNil())
				g.Expect(template.Name).To(Equal(tc.expectedTemplateName))

				if tc.validateTemplateSpec {
					// Validate basic template structure
					g.Expect(template.Spec.Template.Spec.VMSize).To(Equal(tc.expectedVMSize))
					g.Expect(template.Spec.Template.Spec.NetworkInterfaces).To(HaveLen(1))
					g.Expect(template.Spec.Template.Spec.NetworkInterfaces[0].SubnetName).To(Equal(tc.expectedSubnetName))
					g.Expect(template.Spec.Template.Spec.SSHPublicKey).To(Equal(dummySSHKey))

					// Validate image configuration
					if tc.expectedImageID != nil {
						g.Expect(template.Spec.Template.Spec.Image).ToNot(BeNil())
						g.Expect(template.Spec.Template.Spec.Image.ID).To(Equal(tc.expectedImageID))
					}

					if tc.expectedMarketplace != nil {
						g.Expect(template.Spec.Template.Spec.Image).ToNot(BeNil())
						g.Expect(template.Spec.Template.Spec.Image.Marketplace).ToNot(BeNil())
						g.Expect(template.Spec.Template.Spec.Image.Marketplace.ImagePlan.Publisher).To(Equal(tc.expectedMarketplace.ImagePlan.Publisher))
						g.Expect(template.Spec.Template.Spec.Image.Marketplace.ImagePlan.Offer).To(Equal(tc.expectedMarketplace.ImagePlan.Offer))
						g.Expect(template.Spec.Template.Spec.Image.Marketplace.ImagePlan.SKU).To(Equal(tc.expectedMarketplace.ImagePlan.SKU))
						g.Expect(template.Spec.Template.Spec.Image.Marketplace.Version).To(Equal(tc.expectedMarketplace.Version))
					}

					// Validate disk configuration
					if tc.expectedDiskSizeGB != nil {
						g.Expect(template.Spec.Template.Spec.OSDisk.DiskSizeGB).To(Equal(tc.expectedDiskSizeGB))
					}

					if tc.expectedStorageAccount != "" {
						g.Expect(template.Spec.Template.Spec.OSDisk.ManagedDisk).ToNot(BeNil())
						g.Expect(template.Spec.Template.Spec.OSDisk.ManagedDisk.StorageAccountType).To(Equal(tc.expectedStorageAccount))
					}
				}
			}
		})
	}
}
