package azure

import (
	"encoding/json"
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestReconcileCloudConfigWithCredentials uses the actual v1beta1.HostedControlPlane
func TestReconcileCloudConfigWithCredentials(t *testing.T) {
	tests := []struct {
		name              string
		secretName        string
		hcp               *v1beta1.HostedControlPlane
		credentialsSecret *corev1.Secret
		expectedError     bool
		expectedConfig    *AzureConfig
	}{
		{
			name:       "successful reconciliation with Azure disk config",
			secretName: "azure-disk-csi-config",
			hcp: &v1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "aro-hcp-disk"},
				Spec: v1beta1.HostedControlPlaneSpec{
					Platform: v1beta1.PlatformSpec{
						Azure: &v1beta1.AzurePlatformSpec{
							ManagedIdentities: v1beta1.AzureResourceManagedIdentities{
								ControlPlane: v1beta1.ControlPlaneManagedIdentities{
									Disk: v1beta1.ManagedIdentity{
										ClientID:        "disk-client-id",
										CertificateName: "disk-cert",
									},
								},
							},
							SubnetID:        "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
							SecurityGroupID: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
							VnetID:          "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
						},
					},
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					"AZURE_TENANT_ID": []byte("tenant-id"),
				},
			},
			expectedError: false,
			expectedConfig: &AzureConfig{
				Cloud:                        "",
				TenantID:                     "tenant-id",
				UseManagedIdentityExtension:  false,
				SubscriptionID:               "",
				AADClientID:                  "disk-client-id",
				AADClientCertPath:            "/mnt/certs/disk-cert",
				AADClientSecret:              "",
				ResourceGroup:                "",
				Location:                     "",
				VnetName:                     "vnet",
				VnetResourceGroup:            "rg",
				SubnetName:                   "subnet",
				SecurityGroupName:            "nsg",
				SecurityGroupResourceGroup:   "rg",
				RouteTableName:               "",
				CloudProviderBackoff:         true,
				CloudProviderBackoffDuration: 6,
				UseInstanceMetadata:          false,
				LoadBalancerSku:              "standard",
				DisableOutboundSNAT:          true,
				LoadBalancerName:             "",
			},
		},

		{
			name:       "successful reconciliation with Azure file config",
			secretName: "azure-file-csi-config",
			hcp: &v1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "aro-hcp-file"},
				Spec: v1beta1.HostedControlPlaneSpec{
					Platform: v1beta1.PlatformSpec{
						Azure: &v1beta1.AzurePlatformSpec{
							ManagedIdentities: v1beta1.AzureResourceManagedIdentities{
								ControlPlane: v1beta1.ControlPlaneManagedIdentities{
									File: v1beta1.ManagedIdentity{
										ClientID:        "file-client-id",
										CertificateName: "file-cert",
									},
								},
							},
							SubnetID:        "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
							SecurityGroupID: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
							VnetID:          "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
						},
					},
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					"AZURE_TENANT_ID": []byte("tenant-id"),
				},
			},
			expectedError: false,
			expectedConfig: &AzureConfig{
				Cloud:                        "",
				TenantID:                     "tenant-id",
				UseManagedIdentityExtension:  false,
				SubscriptionID:               "",
				AADClientID:                  "file-client-id",
				AADClientCertPath:            "/mnt/certs/file-cert",
				AADClientSecret:              "",
				ResourceGroup:                "",
				Location:                     "",
				VnetName:                     "vnet",
				VnetResourceGroup:            "rg",
				SubnetName:                   "subnet",
				SecurityGroupName:            "nsg",
				SecurityGroupResourceGroup:   "rg",
				RouteTableName:               "",
				CloudProviderBackoff:         true,
				CloudProviderBackoffDuration: 6,
				UseInstanceMetadata:          false,
				LoadBalancerSku:              "standard",
				DisableOutboundSNAT:          true,
				LoadBalancerName:             "",
			},
		},

		{
			name:       "successful reconciliation with Azure cloud config",
			secretName: "azure-cloud-config",
			hcp: &v1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "aro-hcp-cloud"},
				Spec: v1beta1.HostedControlPlaneSpec{
					Platform: v1beta1.PlatformSpec{
						Azure: &v1beta1.AzurePlatformSpec{
							ManagedIdentities: v1beta1.AzureResourceManagedIdentities{
								ControlPlane: v1beta1.ControlPlaneManagedIdentities{
									CloudProvider: v1beta1.ManagedIdentity{
										ClientID:        "cloud-client-id",
										CertificateName: "cloud-cert",
									},
								},
							},
							SubnetID:        "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
							SecurityGroupID: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
							VnetID:          "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
						},
					},
				},
			},
			credentialsSecret: &corev1.Secret{
				Data: map[string][]byte{
					"AZURE_TENANT_ID": []byte("tenant-id"),
				},
			},
			expectedError: false,
			expectedConfig: &AzureConfig{
				Cloud:                        "",
				TenantID:                     "tenant-id",
				UseManagedIdentityExtension:  false,
				SubscriptionID:               "",
				AADClientID:                  "cloud-client-id",
				AADClientCertPath:            "/mnt/certs/cloud-cert",
				AADClientSecret:              "",
				ResourceGroup:                "",
				Location:                     "",
				VnetName:                     "vnet",
				VnetResourceGroup:            "rg",
				SubnetName:                   "subnet",
				SecurityGroupName:            "nsg",
				SecurityGroupResourceGroup:   "rg",
				RouteTableName:               "",
				CloudProviderBackoff:         true,
				CloudProviderBackoffDuration: 6,
				UseInstanceMetadata:          false,
				LoadBalancerSku:              "standard",
				DisableOutboundSNAT:          true,
				LoadBalancerName:             "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock secret to pass into the function
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.secretName,
				},
			}

			err := ReconcileCloudConfigWithCredentials(secret, tt.hcp, tt.credentialsSecret)

			// Check for expected errors
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Check that the secret data is updated correctly
				var actualConfig AzureConfig
				err = json.Unmarshal(secret.Data[CloudConfigKey], &actualConfig)
				assert.NoError(t, err)
				assert.Equal(t, *tt.expectedConfig, actualConfig)
			}
		})
	}
}
