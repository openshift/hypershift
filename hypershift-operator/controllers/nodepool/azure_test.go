package nodepool

import (
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	imageapi "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"

	"github.com/coreos/stream-metadata-go/stream"
	"github.com/coreos/stream-metadata-go/stream/rhcos"
)

func TestAzureMachineTemplateSpec(t *testing.T) {
	testCases := []struct {
		name                             string
		nodePool                         *hyperv1.NodePool
		acrIdentityResourceID            string
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
			expectedErrMsg: "no Azure VM image configured",
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
			name: "When HostedCluster has containerRegistry credentials set it should set UserAssigned identity",
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
			acrIdentityResourceID: "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-mi",
			expectedAzureMachineTemplateSpec: &capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
						Identity: capiazure.VMIdentityUserAssigned,
						UserAssignedIdentities: []capiazure.UserAssignedIdentity{
							{ProviderID: "azure:///subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-mi"},
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
			name: "When HostedCluster has no containerRegistry credentials it should not set UserAssigned identity",
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
			name: "When ipForwarding is Enabled, it should enable IP forwarding on the Azure machine spec",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("testImageID"),
							},
							SubnetID:     "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:       "Standard_D2_v2",
							IPForwarding: hyperv1.AzureIPForwardingEnabled,
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
						EnableIPForwarding:     true,
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
			name: "When ipForwarding is Disabled, it should not enable IP forwarding on the Azure machine spec",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzureNodePoolPlatform{
							Image: hyperv1.AzureVMImage{
								Type:    hyperv1.ImageID,
								ImageID: ptr.To("testImageID"),
							},
							SubnetID:     "/subscriptions/testSubscriptionID/resourceGroups/testResourceGroupName/providers/Microsoft.Network/virtualNetworks/testVnetName/subnets/testSubnetName",
							VMSize:       "Standard_D2_v2",
							IPForwarding: hyperv1.AzureIPForwardingDisabled,
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
			// Opting in is the only way to turn IP forwarding on. This case exists so the
			// negative contract is asserted by name rather than inferred from the nominal
			// cases above.
			name: "When ipForwarding is unset, it should not enable IP forwarding on the Azure machine spec",
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
					ObjectMeta: clusterv1beta1.ObjectMeta{Labels: nil, Annotations: nil},
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
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			azureSpec, err := azureMachineTemplateSpec(tc.nodePool, tc.acrIdentityResourceID)
			if tc.expectedErr {
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrMsg))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(azureSpec).To(Equal(tc.expectedAzureMachineTemplateSpec))
			}
		})
	}
}

// azureIPForwardingTestNodePool returns a NodePool whose Azure platform is populated exactly as a
// pre-ipForwarding object would be, i.e. without the field ever being mentioned. mutate is applied
// afterwards so individual cases can opt in to a specific ipForwarding value.
func azureIPForwardingTestNodePool(mutate func(*hyperv1.AzureNodePoolPlatform)) *hyperv1.NodePool {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-nodepool"},
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
	}
	if mutate != nil {
		mutate(nodePool.Spec.Platform.Azure)
	}
	return nodePool
}

// TestAzureMachineTemplateSpecIPForwardingDefaultsOff asserts that a NodePool written before
// ipForwarding existed is completely unaffected by the field: IP forwarding stays off through a
// full store/decode/reconcile round trip, and the generated machine template name is unchanged.
// The name equality is the machine-checkable form of "deploying this operator version does not
// roll the existing Azure fleet".
func TestAzureMachineTemplateSpecIPForwardingDefaultsOff(t *testing.T) {
	g := NewGomegaWithT(t)

	nodePool := azureIPForwardingTestNodePool(nil)

	spec, err := azureMachineTemplateSpec(nodePool, "")
	g.Expect(err).To(BeNil())
	g.Expect(spec.Template.Spec.EnableIPForwarding).To(BeFalse(), "an unset ipForwarding must never enable IP forwarding")

	// Round trip the platform through the API wire format, the way a stored object is decoded
	// before every reconcile. The key must not appear, and decoding it back must not flip the
	// CAPZ field on.
	platformJSON, err := json.Marshal(nodePool.Spec.Platform.Azure)
	g.Expect(err).To(BeNil())
	g.Expect(string(platformJSON)).ToNot(ContainSubstring("ipForwarding"), "an unset ipForwarding must not be serialized")

	decodedPlatform := &hyperv1.AzureNodePoolPlatform{}
	g.Expect(json.Unmarshal(platformJSON, decodedPlatform)).To(Succeed())
	g.Expect(decodedPlatform.IPForwarding).To(BeEmpty())

	decodedNodePool := azureIPForwardingTestNodePool(nil)
	decodedNodePool.Spec.Platform.Azure = decodedPlatform
	decodedSpec, err := azureMachineTemplateSpec(decodedNodePool, "")
	g.Expect(err).To(BeNil())
	g.Expect(decodedSpec.Template.Spec.EnableIPForwarding).To(BeFalse())

	// The generated template name must be identical whether ipForwarding is absent or explicitly
	// Disabled: neither value may produce a new AzureMachineTemplate and therefore a rollout.
	hashNameGenerator := func(spec any) (string, error) {
		specJSON, err := json.Marshal(spec)
		if err != nil {
			return "", err
		}
		return getName(nodePool.GetName(), supportutil.HashSimple(specJSON), validation.DNS1123SubdomainMaxLength), nil
	}
	templateNameFor := func(np *hyperv1.NodePool) string {
		t.Helper()
		capi := &CAPI{
			Token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: np,
					rolloutConfig: &rolloutConfig{
						releaseImage: createMockReleaseImage("4.20.0", true),
					},
				},
			},
		}
		template, err := capi.azureMachineTemplate(t.Context(), hashNameGenerator)
		g.Expect(err).To(BeNil())
		return template.Name
	}

	unsetName := templateNameFor(nodePool)
	disabledName := templateNameFor(azureIPForwardingTestNodePool(func(platform *hyperv1.AzureNodePoolPlatform) {
		platform.IPForwarding = hyperv1.AzureIPForwardingDisabled
	}))
	enabledName := templateNameFor(azureIPForwardingTestNodePool(func(platform *hyperv1.AzureNodePoolPlatform) {
		platform.IPForwarding = hyperv1.AzureIPForwardingEnabled
	}))

	g.Expect(disabledName).To(Equal(unsetName), "setting ipForwarding to Disabled must not change the machine template name, or existing NodePools would roll")
	g.Expect(enabledName).ToNot(Equal(unsetName), "setting ipForwarding to Enabled must change the machine template name so the nodes are replaced")
}

// TestAzureMachineTemplateSpecIPForwardingSerialization guards the no-fleet-rollout property at the
// level that actually determines it: the marshaled machine template spec, which is hashed into the
// template name by machineTemplateBuilders (capi.go:880-889). A regression here — a dropped
// omitempty in a CAPZ vendor bump, or a CRD default on ipForwarding — would roll every Azure
// NodePool in the fleet, which the struct-level table test above cannot detect.
func TestAzureMachineTemplateSpecIPForwardingSerialization(t *testing.T) {
	g := NewGomegaWithT(t)

	marshalSpecFor := func(ipForwarding hyperv1.AzureIPForwarding) string {
		t.Helper()
		spec, err := azureMachineTemplateSpec(azureIPForwardingTestNodePool(func(platform *hyperv1.AzureNodePoolPlatform) {
			platform.IPForwarding = ipForwarding
		}), "")
		g.Expect(err).To(BeNil())
		specJSON, err := json.Marshal(spec)
		g.Expect(err).To(BeNil())
		return string(specJSON)
	}

	unsetJSON := marshalSpecFor("")
	g.Expect(unsetJSON).ToNot(ContainSubstring("enableIPForwarding"))

	disabledJSON := marshalSpecFor(hyperv1.AzureIPForwardingDisabled)
	g.Expect(disabledJSON).ToNot(ContainSubstring("enableIPForwarding"))
	g.Expect(disabledJSON).To(Equal(unsetJSON), "Disabled must marshal identically to unset so the template hash is unchanged")

	enabledJSON := marshalSpecFor(hyperv1.AzureIPForwardingEnabled)
	g.Expect(enabledJSON).To(ContainSubstring(`"enableIPForwarding":true`))
}

func TestAzureMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                   string
		nodePool               *hyperv1.NodePool
		releaseImage           *releaseinfo.ReleaseImage
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
							VMSize:   "Standard_D4s_v5",
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
			expectedVMSize:         "Standard_D4s_v5",
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
							VMSize:   "Standard_D2s_v5",
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
			expectedVMSize:       "Standard_D2s_v5",
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
							VMSize:   "Standard_D2s_v5",
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
							VMSize:   "Standard_D2s_v5",
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
							VMSize:   "Standard_D2s_v5",
							OSDisk: hyperv1.AzureNodePoolOSDisk{
								SizeGiB:                30,
								DiskStorageAccountType: "Standard_LRS",
							},
						},
					},
				},
			},
			releaseImage: createMockReleaseImage("4.20.0", false),
			templateNameGenerator: func(spec any) (string, error) {
				return "should-not-be-called", nil
			},
			expectedErr:    true,
			expectedErrMsg: "no Azure VM image configured",
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
							VMSize:           "Standard_D2s_v5",
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
			expectedVMSize:       "Standard_D2s_v5",
			expectedSubnetName:   "subnet-worker",
			expectedImageID:      ptr.To("test-image"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Use test-specific release image or default to one with marketplace metadata
			releaseImg := tc.releaseImage
			if releaseImg == nil {
				releaseImg = createMockReleaseImage("4.20.0", true)
			}

			// Create a CAPI instance with minimal required fields
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool: tc.nodePool,
						rolloutConfig: &rolloutConfig{
							releaseImage: releaseImg,
						},
					},
				},
			}

			// Call the method under test
			template, err := capi.azureMachineTemplate(t.Context(), tc.templateNameGenerator)

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

func TestDefaultAzureNodePoolImage(t *testing.T) {
	testCases := []struct {
		name                     string
		nodePool                 *hyperv1.NodePool
		releaseImage             *releaseinfo.ReleaseImage
		streamName               string
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
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									ImageGeneration: ptr.To(hyperv1.Gen1),
								},
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
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									ImageGeneration: ptr.To(hyperv1.Gen2),
								},
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
			name: "skip defaulting when RHELCoreOSExtensions is nil",
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
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.20.0"},
				},
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"x86_64": {
							Images: stream.Images{},
						},
					},
				},
			},
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
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									ImageGeneration: ptr.To(hyperv1.AzureVMImageGeneration("Gen3")),
								},
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
				Publisher:       "azureopenshift",
				Offer:           "aro4",
				SKU:             "419-v2",
				Version:         "419.6.20250523",
				ImageGeneration: ptr.To(hyperv1.Gen2),
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
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									ImageGeneration: ptr.To(hyperv1.Gen1),
								},
							},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher:       "azureopenshift",
				Offer:           "aro4",
				SKU:             "aro_419",
				Version:         "419.6.20250523",
				ImageGeneration: ptr.To(hyperv1.Gen1),
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
				Publisher:       "azureopenshift",
				Offer:           "aro4",
				SKU:             "419-v2",
				Version:         "419.6.20250523",
				ImageGeneration: ptr.To(hyperv1.Gen2),
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
								Type: hyperv1.AzureMarketplace,
								AzureMarketplace: &hyperv1.AzureMarketplaceImage{
									ImageGeneration: ptr.To(hyperv1.Gen1),
								},
							},
						},
					},
				},
			},
			releaseImage:      createMockReleaseImage("4.20.0", true),
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher:       "azureopenshift",
				Offer:           "aro4",
				SKU:             "aro_419",
				Version:         "419.6.20250523",
				ImageGeneration: ptr.To(hyperv1.Gen1),
			},
		},
		{
			name: "When named stream is used with multi-stream ReleaseImage it should resolve marketplace from the named stream",
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
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.20.0"},
				},
				OSStreams: map[string]*stream.Stream{
					"rhel-9": {
						Architectures: map[string]stream.Arch{
							"x86_64": {
								RHELCoreOSExtensions: &rhcos.Extensions{
									AzureDisk: &rhcos.AzureDisk{
										Release: "9.6.20250701-0",
										URL:     "https://rhcos.blob.core.windows.net/imagebucket/rhcos-9.6.20250701-0-azure.x86_64.vhd",
									},
									Marketplace: &rhcos.Marketplace{
										Azure: &rhcos.AzureMarketplace{
											NoPurchasePlan: &rhcos.AzureMarketplaceImages{
												Gen2: &rhcos.AzureMarketplaceImage{
													Publisher: "azureopenshift",
													Offer:     "aro4",
													SKU:       "aro_rhel9_420-v2",
													Version:   "420.9.20250701",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			streamName:        "rhel-9",
			expectedImageType: hyperv1.AzureMarketplace,
			expectedMarketplaceImage: &hyperv1.AzureMarketplaceImage{
				Publisher:       "azureopenshift",
				Offer:           "aro4",
				SKU:             "aro_rhel9_420-v2",
				Version:         "420.9.20250701",
				ImageGeneration: ptr.To(hyperv1.Gen2),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := defaultAzureNodePoolImage(tc.nodePool, tc.releaseImage, tc.streamName)

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
	architecture := stream.Arch{
		Artifacts: map[string]stream.PlatformArtifacts{},
		Images:    stream.Images{},
		RHELCoreOSExtensions: &rhcos.Extensions{
			AzureDisk: &rhcos.AzureDisk{
				Release: "9.6.20250701-0",
				URL:     "https://rhcos.blob.core.windows.net/imagebucket/rhcos-9.6.20250701-0-azure.x86_64.vhd",
			},
		},
	}

	if hasMarketplaceMetadata {
		architecture.RHELCoreOSExtensions.Marketplace = &rhcos.Marketplace{
			Azure: &rhcos.AzureMarketplace{
				NoPurchasePlan: &rhcos.AzureMarketplaceImages{
					Gen1: &rhcos.AzureMarketplaceImage{
						Publisher: "azureopenshift",
						Offer:     "aro4",
						SKU:       "aro_419",
						Version:   "419.6.20250523",
					},
					Gen2: &rhcos.AzureMarketplaceImage{
						Publisher: "azureopenshift",
						Offer:     "aro4",
						SKU:       "419-v2",
						Version:   "419.6.20250523",
					},
				},
			},
		}
	}

	architectures := map[string]stream.Arch{
		"x86_64":  architecture,
		"aarch64": architecture, // ARM64 uses the same marketplace metadata
	}

	streamMetadata := &stream.Stream{
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
