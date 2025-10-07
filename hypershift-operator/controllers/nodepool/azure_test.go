package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestAzureAdditionalTags(t *testing.T) {

	testCases := []struct {
		name           string
		hostedCluster  *hyperv1.HostedCluster
		nodePool       *hyperv1.NodePool
		expectedTagMap map[string]string
	}{
		{
			name:           "no tags on hostedcluster or nodepool",
			hostedCluster:  &hyperv1.HostedCluster{},
			nodePool:       &hyperv1.NodePool{},
			expectedTagMap: nil,
		},
		{
			name: "hostecluster tags",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "hostedcluster",
									Value: "true",
								},
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{},
			expectedTagMap: map[string]string{
				"hostedcluster": "true",
			},
		},
		{
			name:          "nodepool tags",
			hostedCluster: &hyperv1.HostedCluster{},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Azure: &hyperv1.AzureNodePoolPlatform{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "nodepool",
									Value: "true",
								},
							},
						},
					},
				},
			},
			expectedTagMap: map[string]string{
				"nodepool": "true",
			},
		},
		{
			name: "hostedcluster and nodepool tags",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "hostedcluster",
									Value: "true",
								},
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Azure: &hyperv1.AzureNodePoolPlatform{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "nodepool",
									Value: "true",
								},
							},
						},
					},
				},
			},
			expectedTagMap: map[string]string{
				"hostedcluster": "true",
				"nodepool":      "true",
			},
		},
		{
			name: "hostedcluster and nodepool overlapping tag",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "overlap",
									Value: "true",
								},
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Azure: &hyperv1.AzureNodePoolPlatform{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "overlap",
									Value: "false",
								},
							},
						},
					},
				},
			},
			expectedTagMap: map[string]string{
				"overlap": "true",
			},
		},
		{
			name: "hostedcluster and nodepool overlap and individual tags",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "hostedcluster",
									Value: "true",
								},
								{
									Key:   "overlap",
									Value: "true",
								},
							},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Azure: &hyperv1.AzureNodePoolPlatform{
							ResourceTags: []hyperv1.AzureResourceTag{
								{
									Key:   "nodepool",
									Value: "true",
								},
								{
									Key:   "overlap",
									Value: "false",
								},
							},
						},
					},
				},
			},
			expectedTagMap: map[string]string{
				"hostedcluster": "true",
				"nodepool":      "true",
				"overlap":       "true",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			resultingTagMap := azureAdditionalTags(tc.hostedCluster, tc.nodePool)

			g.Expect(resultingTagMap).To(Equal(tc.expectedTagMap))
		})
	}
}

func TestAzureMachineTemplateSpec(t *testing.T) {
	defaultHostedCluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{},
			},
		},
	}

	testCases := []struct {
		name                             string
		hostedCluster                    *hyperv1.HostedCluster
		nodePool                         *hyperv1.NodePool
		expectedAzureMachineTemplateSpec *capiazure.AzureMachineTemplateSpec
		expectedErr                      bool
		expectedErrMsg                   string
	}{
		{
			name:          "nominal case without managed identity",
			hostedCluster: defaultHostedCluster,
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
		{
			name: "tags from nodepool get copied",
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
							ResourceTags: []hyperv1.AzureResourceTag{
								{Key: "key", Value: "value"},
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
						AdditionalTags:         map[string]string{"key": "value"},
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
			name: "tags from cluster get copied",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{Key: "cluster-key", Value: "cluster-value"},
							},
						},
					},
				},
			},
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
						AdditionalTags:         map[string]string{"cluster-key": "cluster-value"},
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
			name: "cluster tags take precedence over nodepool tags",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							ResourceTags: []hyperv1.AzureResourceTag{
								{Key: "cluster-only", Value: "cluster-value"},
								{Key: "conflict", Value: "cluster-wins"},
							},
						},
					},
				},
			},
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
							ResourceTags: []hyperv1.AzureResourceTag{
								{Key: "nodepool-only", Value: "nodepool-value"},
								{Key: "conflict", Value: "nodepool-loses"},
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
						AdditionalTags:         map[string]string{"cluster-only": "cluster-value", "nodepool-only": "nodepool-value", "conflict": "cluster-wins"},
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
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hostedCluster := tc.hostedCluster
			if hostedCluster == nil {
				hostedCluster = defaultHostedCluster
			}

			azureSpec, err := azureMachineTemplateSpec(hostedCluster, tc.nodePool)
			if tc.expectedErr {
				g.Expect(err.Error()).To(Equal(tc.expectedErrMsg))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(azureSpec).To(Equal(tc.expectedAzureMachineTemplateSpec))
			}
		})
	}
}
