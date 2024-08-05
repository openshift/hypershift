package nodepool

import (
	"k8s.io/utils/ptr"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestAzureMachineTemplateSpec(t *testing.T) {
	testAzureMachineTemplateSpec := capiazure.AzureMachineTemplateSpec{
		Template: capiazure.AzureMachineTemplateResource{
			Spec: capiazure.AzureMachineSpec{
				SSHPublicKey: "asdf",
			},
		},
	}

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
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
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
						SSHPublicKey:           "asdf",
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
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							MachineIdentityID:      "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
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
						Identity: "UserAssigned",
						UserAssignedIdentities: []capiazure.UserAssignedIdentity{
							{
								ProviderID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
							},
						},
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
						SSHPublicKey:           "asdf",
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
								AzureMarketplace: &hyperv1.MarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							MachineIdentityID:      "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
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
						Identity: "UserAssigned",
						UserAssignedIdentities: []capiazure.UserAssignedIdentity{
							{
								ProviderID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
							},
						},
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
						SSHPublicKey:           "asdf",
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
							AvailabilityZone:    "eastus",
							DiskEncryptionSetID: "testDES_ID",
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.MarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							EnableEphemeralOSDisk:  true,
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "Managed",
							},
							MachineIdentityID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
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
						FailureDomain: ptr.To("eastus"),
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
						Identity: "UserAssigned",
						UserAssignedIdentities: []capiazure.UserAssignedIdentity{
							{
								ProviderID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
							},
						},
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
						SSHPublicKey:           "asdf",
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
						SecurityProfile: &capiazure.SecurityProfile{
							EncryptionAtHost: ptr.To(true),
							SecurityType:     "",
							UefiSettings:     nil,
						},
						SubnetName:   "",
						DNSServers:   nil,
						VMExtensions: nil,
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
							DiskEncryptionSetID: "testDES_ID",
							Image: hyperv1.AzureVMImage{
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.MarketplaceImage{
									Publisher: "testPublisher",
									Offer:     "testOffer",
									SKU:       "testSKU",
									Version:   "testVersion",
								},
							},
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							EnableEphemeralOSDisk:  true,
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								StorageAccountURI:  "www.test.com",
							},
							MachineIdentityID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
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
						Identity: "UserAssigned",
						UserAssignedIdentities: []capiazure.UserAssignedIdentity{
							{
								ProviderID: "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.ManagedIdentity/userAssignedIdentities/testIdentity",
							},
						},
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
						SSHPublicKey:           "asdf",
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
						SecurityProfile: &capiazure.SecurityProfile{
							EncryptionAtHost: ptr.To(true),
							SecurityType:     "",
							UefiSettings:     nil,
						},
						SubnetName:   "",
						DNSServers:   nil,
						VMExtensions: nil,
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
							DiskEncryptionSetID:    "testDES_ID",
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							EnableEphemeralOSDisk:  true,
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								StorageAccountURI:  "www.test.com",
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
							DiskEncryptionSetID:    "testDES_ID",
							SubnetID:               "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/",
							VMSize:                 "Standard_D2_v2",
							DiskSizeGB:             30,
							DiskStorageAccountType: "Standard_LRS",
							EnableEphemeralOSDisk:  true,
							Diagnostics: &hyperv1.Diagnostics{
								StorageAccountType: "UserManaged",
								StorageAccountURI:  "www.test.com",
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
			cluster := &hyperv1.HostedCluster{}

			azureSpec, err := azureMachineTemplateSpec(cluster, tc.nodePool, testAzureMachineTemplateSpec)
			if tc.expectedErr {
				g.Expect(err.Error()).To(Equal(tc.expectedErrMsg))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(azureSpec).To(Equal(tc.expectedAzureMachineTemplateSpec))
			}
		})
	}
}
