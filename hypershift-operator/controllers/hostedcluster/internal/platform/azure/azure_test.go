package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	azurecloud "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
)

func TestReconcileAzureClusterIdentity(t *testing.T) {
	// Set up initial conditions
	hcVersion := semver.MustParse("4.19.0")
	controlPlaneNamespace := "test-namespace"
	initialAzureClusterIdentity := &capiazure.AzureClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-identity",
			Namespace: controlPlaneNamespace,
		},
	}

	testCases := []struct {
		name                         string
		isManagedService             bool
		hc                           *hyperv1.HostedCluster
		expectedAzureClusterIdentity *capiazure.AzureClusterIdentity
	}{
		{
			name:             "when MANAGED_SERVICE is set to AROHCP, it should reconcile AzureClusterIdentity as UserAssignedIdentityCredential",
			isManagedService: true,
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: controlPlaneNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant-id",
							Cloud:    "AzurePublicCloud",
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: "ManagedIdentities",
								ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
									ControlPlane: hyperv1.ControlPlaneManagedIdentities{
										NodePoolManagement: hyperv1.ManagedIdentity{
											CredentialsSecretName: "credentials",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedAzureClusterIdentity: &capiazure.AzureClusterIdentity{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-identity",
					Namespace: controlPlaneNamespace,
				},
				Spec: capiazure.AzureClusterIdentitySpec{
					TenantID:                                 "test-tenant-id",
					UserAssignedIdentityCredentialsCloudType: "public",
					UserAssignedIdentityCredentialsPath:      config.ManagedAzureCertificatePath + "credentials",
					Type:                                     capiazure.UserAssignedIdentityCredential,
				},
			},
		},
		{
			name:             "when MANAGED_SERVICE is not set, it should reconcile AzureClusterIdentity as WorkloadIdentity",
			isManagedService: false,
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: controlPlaneNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant-id",
							Cloud:    "AzurePublicCloud",
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: "WorkloadIdentities",
								WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
									NodePoolManagement: hyperv1.WorkloadIdentity{
										ClientID: "test-client-id",
									},
								},
							},
						},
					},
				},
			},
			expectedAzureClusterIdentity: &capiazure.AzureClusterIdentity{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-identity",
					Namespace: controlPlaneNamespace,
				},
				Spec: capiazure.AzureClusterIdentitySpec{
					ClientID: "test-client-id",
					TenantID: "test-tenant-id",
					Type:     capiazure.WorkloadIdentity,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.isManagedService {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			}

			err := reconcileAzureClusterIdentity(tc.hc, initialAzureClusterIdentity, controlPlaneNamespace, &hcVersion)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(initialAzureClusterIdentity.Spec.TenantID).Should(Equal(tc.expectedAzureClusterIdentity.Spec.TenantID))
			g.Expect(initialAzureClusterIdentity.Spec.Type).Should(Equal(tc.expectedAzureClusterIdentity.Spec.Type))

			if tc.isManagedService {
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath).Should(Equal(tc.expectedAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath))
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType).Should(Equal(tc.expectedAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType))
			} else {
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath).Should(BeEmpty())
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType).Should(BeEmpty())
				g.Expect(initialAzureClusterIdentity.Spec.ClientID).Should(Equal(tc.expectedAzureClusterIdentity.Spec.ClientID))
				g.Expect(initialAzureClusterIdentity.Spec.Type).Should(Equal(tc.expectedAzureClusterIdentity.Spec.Type))
			}
		})
	}
}

func TestParseCloudType(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedOutput string
		expectedError  bool
	}{
		{
			name:           "when input is AzurePublicCloud, expected output is public",
			input:          "AzurePublicCloud",
			expectedOutput: "public",
			expectedError:  false,
		},
		{
			name:           "when input is AzureUSGovernmentCloud, expected output is usgovernment",
			input:          "AzureUSGovernmentCloud",
			expectedOutput: "usgovernment",
			expectedError:  false,
		},
		{
			name:           "when input is AzureChinaCloud, expected output is china",
			input:          "AzureChinaCloud",
			expectedOutput: "china",
			expectedError:  false,
		},
		{
			name:           "when input is an invalid cloud type, expect error",
			input:          "AzureGermanCloud",
			expectedOutput: "",
			expectedError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			azureCloudType, err := parseCloudType(tc.input)
			g.Expect(azureCloudType).To(Equal(tc.expectedOutput))
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestReconcileCredentials(t *testing.T) {
	g := NewWithT(t)

	// Helper function to create a test hosted cluster
	createTestHostedCluster := func(selfManaged bool, workloadIdentities *hyperv1.AzureWorkloadIdentities) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "test-infra-id",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
					Azure: &hyperv1.AzurePlatformSpec{
						Location:                  "eastus",
						ResourceGroupName:         "test-rg",
						SubscriptionID:            "sub-123",
						TenantID:                  "tenant-456",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{},
					},
				},
				Capabilities: &hyperv1.Capabilities{},
			},
		}

		if selfManaged && workloadIdentities != nil {
			hc.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
				AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
				WorkloadIdentities:            workloadIdentities,
			}
		}

		return hc
	}

	// Mock createOrUpdate function
	var createdSecrets []*corev1.Secret
	mockCreateOrUpdate := func(ctx context.Context, client client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return controllerutil.OperationResultNone, fmt.Errorf("expected Secret, got %T", obj)
		}

		// Call the mutate function to set up the secret data
		if err := mutate(); err != nil {
			return controllerutil.OperationResultNone, err
		}

		// Store the secret for validation
		createdSecrets = append(createdSecrets, secret.DeepCopy())
		return controllerutil.OperationResultCreated, nil
	}

	tests := []struct {
		name                 string
		managedService       string
		hcluster             *hyperv1.HostedCluster
		expectedSecretsCount int
		expectedError        bool
		validateSecrets      func(secrets []*corev1.Secret)
	}{
		{
			name:           "self-managed Azure with workload identities creates all credential secrets",
			managedService: "",
			hcluster: createTestHostedCluster(true, &hyperv1.AzureWorkloadIdentities{
				CloudProvider: hyperv1.WorkloadIdentity{
					ClientID: "cloud-provider-client-id",
				},
				NodePoolManagement: hyperv1.WorkloadIdentity{
					ClientID: "nodepool-mgmt-client-id",
				},
				Ingress: hyperv1.WorkloadIdentity{
					ClientID: "ingress-client-id",
				},
				ImageRegistry: hyperv1.WorkloadIdentity{
					ClientID: "registry-client-id",
				},
				Disk: hyperv1.WorkloadIdentity{
					ClientID: "disk-client-id",
				},
				File: hyperv1.WorkloadIdentity{
					ClientID: "file-client-id",
				},
				Network: hyperv1.WorkloadIdentity{
					ClientID: "network-client-id",
				},
			}),
			expectedSecretsCount: 3, // ingress, image-registry, cncc (disk/file managed by control-plane-operator)
			expectedError:        false,
			validateSecrets: func(secrets []*corev1.Secret) {
				// Check base data is present in all secrets
				for _, secret := range secrets {
					g.Expect(secret.Data["azure_region"]).To(Equal([]byte("eastus")))
					g.Expect(secret.Data["azure_resource_prefix"]).To(Equal([]byte("test-cluster-test-infra-id")))
					g.Expect(secret.Data["azure_resourcegroup"]).To(Equal([]byte("test-rg")))
					g.Expect(secret.Data["azure_subscription_id"]).To(Equal([]byte("sub-123")))
					g.Expect(secret.Data["azure_tenant_id"]).To(Equal([]byte("tenant-456")))
					g.Expect(secret.Data["azure_federated_token_file"]).To(Equal([]byte("/var/run/secrets/openshift/serviceaccount/token")))
				}

				// Find and validate specific secrets
				secretsByName := make(map[string]*corev1.Secret)
				for _, secret := range secrets {
					secretsByName[secret.Name] = secret
				}

				// Validate client IDs are set correctly based on the secret name
				// Note: disk/file CSI credentials are managed by control-plane-operator, not here
				expectedClientIDs := map[string]string{
					"azure-ingress-credentials":             "ingress-client-id",
					"azure-image-registry-credentials":      "registry-client-id",
					"cloud-network-config-controller-creds": "network-client-id",
				}

				for secretName, expectedClientID := range expectedClientIDs {
					secret, exists := secretsByName[secretName]
					g.Expect(exists).To(BeTrue(), fmt.Sprintf("Secret %s should exist", secretName))
					g.Expect(secret.Data["azure_client_id"]).To(Equal([]byte(expectedClientID)))
				}
			},
		},
		{
			name:           "self-managed Azure with disabled capabilities skips appropriate secrets",
			managedService: "",
			hcluster: func() *hyperv1.HostedCluster {
				hc := createTestHostedCluster(true, &hyperv1.AzureWorkloadIdentities{
					CloudProvider: hyperv1.WorkloadIdentity{
						ClientID: "cloud-provider-client-id",
					},
					NodePoolManagement: hyperv1.WorkloadIdentity{
						ClientID: "nodepool-mgmt-client-id",
					},
					Ingress: hyperv1.WorkloadIdentity{
						ClientID: "ingress-client-id",
					},
					ImageRegistry: hyperv1.WorkloadIdentity{
						ClientID: "registry-client-id",
					},
					Disk: hyperv1.WorkloadIdentity{
						ClientID: "disk-client-id",
					},
					File: hyperv1.WorkloadIdentity{
						ClientID: "file-client-id",
					},
					Network: hyperv1.WorkloadIdentity{
						ClientID: "network-client-id",
					},
				})
				// Disable ingress and image registry capabilities
				hc.Spec.Capabilities.Disabled = []hyperv1.OptionalCapability{
					hyperv1.IngressCapability,
					hyperv1.ImageRegistryCapability,
				}
				return hc
			}(),
			expectedSecretsCount: 1, // Only cncc (disk/file managed by control-plane-operator, ingress/image-registry disabled)
			expectedError:        false,
			validateSecrets: func(secrets []*corev1.Secret) {
				secretNames := make([]string, len(secrets))
				for i, secret := range secrets {
					secretNames[i] = secret.Name
				}

				// Should not contain ingress or image-registry secrets (disabled capabilities)
				g.Expect(secretNames).ToNot(ContainElement("azure-ingress-credentials"))
				g.Expect(secretNames).ToNot(ContainElement("azure-image-registry-credentials"))

				// Should only contain CNCC secret (disk/file managed by control-plane-operator)
				g.Expect(secretNames).To(ContainElement("cloud-network-config-controller-creds"))
			},
		},
		{
			name:                 "managed Azure (ARO-HCP) does not create workload identity secrets",
			managedService:       hyperv1.AroHCP,
			hcluster:             createTestHostedCluster(false, nil),
			expectedSecretsCount: 1, // Only CNCC secret should be created
			expectedError:        false,
			validateSecrets: func(secrets []*corev1.Secret) {
				g.Expect(len(secrets)).To(Equal(1))
				g.Expect(secrets[0].Name).To(Equal("cloud-network-config-controller-creds"))
				// Should not have azure_client_id for managed Azure CNCC
				g.Expect(secrets[0].Data["azure_client_id"]).To(BeNil())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset the created secrets slice for each test
			createdSecrets = []*corev1.Secret{}

			// Set environment variable if needed
			if tt.managedService != "" {
				t.Setenv("MANAGED_SERVICE", tt.managedService)
			}

			// Create the Azure platform instance
			azure := New("test-utilities-image", "test-capi-image", nil)

			// Create a fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				Build()

			// Call the function under test
			err := azure.ReconcileCredentials(
				t.Context(),
				fakeClient,
				mockCreateOrUpdate,
				tt.hcluster,
				"test-control-plane-ns",
			)

			// Validate error expectation
			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			// Validate secret count
			g.Expect(len(createdSecrets)).To(Equal(tt.expectedSecretsCount))

			// Run custom validations
			if tt.validateSecrets != nil {
				tt.validateSecrets(createdSecrets)
			}
		})
	}
}

func TestReconcileKMSConfigSecret(t *testing.T) {
	baseHC := func() *hyperv1.HostedCluster {
		return &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "test-infra",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
					Azure: &hyperv1.AzurePlatformSpec{
						Cloud:             "AzurePublicCloud",
						TenantID:          "test-tenant-id",
						SubscriptionID:    "test-sub-id",
						ResourceGroupName: "test-rg",
						Location:          "eastus",
					},
				},
				SecretEncryption: &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.KMS,
					KMS: &hyperv1.KMSSpec{
						Provider: hyperv1.AZURE,
						Azure: &hyperv1.AzureKMSSpec{
							ActiveKey: hyperv1.AzureKMSKey{
								KeyVaultName: "test-vault",
								KeyName:      "test-key",
								KeyVersion:   "v1",
							},
						},
					},
				},
			},
		}
	}

	testCases := []struct {
		name           string
		managedService string
		hc             *hyperv1.HostedCluster
		expectErr      bool
		validate       func(g Gomega, cfg azurecloud.AzureConfig)
	}{
		{
			name:           "When ARO HCP it should set AADMSIDataPlaneIdentityPath",
			managedService: hyperv1.AroHCP,
			hc: func() *hyperv1.HostedCluster {
				hc := baseHC()
				hc.Spec.SecretEncryption.KMS.Azure.KMS = hyperv1.ManagedIdentity{
					CredentialsSecretName: "kms-creds",
				}
				return hc
			}(),
			validate: func(g Gomega, cfg azurecloud.AzureConfig) {
				g.Expect(cfg.AADMSIDataPlaneIdentityPath).To(Equal(config.ManagedAzureCertificatePath + "kms-creds"))
				g.Expect(cfg.UseWorkloadIdentityExtension).To(BeFalse())
				g.Expect(cfg.AADClientID).To(BeEmpty())
			},
		},
		{
			name: "When self-managed Azure with workload identities it should set federated identity fields",
			hc: func() *hyperv1.HostedCluster {
				hc := baseHC()
				hc.Spec.SecretEncryption.KMS.Azure.WorkloadIdentity = hyperv1.WorkloadIdentity{
					ClientID: "kms-client-id",
				}
				return hc
			}(),
			validate: func(g Gomega, cfg azurecloud.AzureConfig) {
				g.Expect(cfg.UseWorkloadIdentityExtension).To(BeTrue())
				g.Expect(cfg.AADClientID).To(BeEmpty())
				g.Expect(cfg.AADMSIDataPlaneIdentityPath).To(BeEmpty())
			},
		},
		{
			name:      "When Azure KMS without any credentials it should return an error",
			hc:        baseHC(),
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.managedService != "" {
				t.Setenv("MANAGED_SERVICE", tc.managedService)
			}

			secret := &corev1.Secret{}
			err := reconcileKMSConfigSecret(secret, tc.hc)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(secret.Data).To(HaveKey(azurecloud.CloudConfigKey))

			var cfg azurecloud.AzureConfig
			err = json.Unmarshal(secret.Data[azurecloud.CloudConfigKey], &cfg)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify common base fields
			g.Expect(cfg.Cloud).To(Equal("AzurePublicCloud"))
			g.Expect(cfg.TenantID).To(Equal("test-tenant-id"))
			g.Expect(cfg.SubscriptionID).To(Equal("test-sub-id"))
			g.Expect(cfg.ResourceGroup).To(Equal("test-rg"))
			g.Expect(cfg.Location).To(Equal("eastus"))
			g.Expect(cfg.LoadBalancerName).To(Equal("test-infra"))
			g.Expect(cfg.CloudProviderBackoff).To(BeTrue())
			g.Expect(cfg.LoadBalancerSku).To(Equal("standard"))

			tc.validate(g, cfg)
		})
	}
}

func TestValidateWorkloadIdentityConfig(t *testing.T) {
	tests := []struct {
		name           string
		hcluster       *hyperv1.HostedCluster
		expectNil      bool
		expectStatus   metav1.ConditionStatus
		expectReason   string
		expectContains string
	}{
		{
			name: "non-Azure platform returns nil",
			hcluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectNil: true,
		},
		{
			name: "Azure with ManagedIdentities auth returns nil",
			hcluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
							},
						},
					},
				},
			},
			expectNil: true,
		},
		{
			name: "WorkloadIdentities auth with nil WorkloadIdentities returns not configured",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
							},
						},
					},
				},
			},
			expectNil:      false,
			expectStatus:   metav1.ConditionFalse,
			expectReason:   hyperv1.AzureWorkloadIdentityNotConfiguredReason,
			expectContains: "not configured",
		},
		{
			name: "WorkloadIdentities with missing clientIDs returns incomplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 5},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
								WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
									CloudProvider:      hyperv1.WorkloadIdentity{ClientID: "client-1"},
									NodePoolManagement: hyperv1.WorkloadIdentity{ClientID: "client-2"},
									Ingress:            hyperv1.WorkloadIdentity{ClientID: ""},
									ImageRegistry:      hyperv1.WorkloadIdentity{ClientID: "client-4"},
									Disk:               hyperv1.WorkloadIdentity{ClientID: "client-5"},
									File:               hyperv1.WorkloadIdentity{ClientID: ""},
									Network:            hyperv1.WorkloadIdentity{ClientID: "client-7"},
								},
							},
						},
					},
				},
			},
			expectNil:      false,
			expectStatus:   metav1.ConditionFalse,
			expectReason:   hyperv1.AzureWorkloadIdentityIncompleteReason,
			expectContains: "ingress, file",
		},
		{
			name: "When Private Link is configured without controlPlaneOperator clientID, it should report incomplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 6},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Private: hyperv1.AzurePrivateSpec{
								Type: hyperv1.AzurePrivateTypePrivateLink,
							},
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
								WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
									CloudProvider:      hyperv1.WorkloadIdentity{ClientID: "client-1"},
									NodePoolManagement: hyperv1.WorkloadIdentity{ClientID: "client-2"},
									Ingress:            hyperv1.WorkloadIdentity{ClientID: "client-3"},
									ImageRegistry:      hyperv1.WorkloadIdentity{ClientID: "client-4"},
									Disk:               hyperv1.WorkloadIdentity{ClientID: "client-5"},
									File:               hyperv1.WorkloadIdentity{ClientID: "client-6"},
									Network:            hyperv1.WorkloadIdentity{ClientID: "client-7"},
								},
							},
						},
					},
				},
			},
			expectNil:      false,
			expectStatus:   metav1.ConditionFalse,
			expectReason:   hyperv1.AzureWorkloadIdentityIncompleteReason,
			expectContains: "controlPlaneOperator",
		},
		{
			name: "When Private Link is configured with controlPlaneOperator clientID, it should return valid",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 6},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Private: hyperv1.AzurePrivateSpec{
								Type: hyperv1.AzurePrivateTypePrivateLink,
							},
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
								WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
									CloudProvider:        hyperv1.WorkloadIdentity{ClientID: "client-1"},
									NodePoolManagement:   hyperv1.WorkloadIdentity{ClientID: "client-2"},
									Ingress:              hyperv1.WorkloadIdentity{ClientID: "client-3"},
									ImageRegistry:        hyperv1.WorkloadIdentity{ClientID: "client-4"},
									Disk:                 hyperv1.WorkloadIdentity{ClientID: "client-5"},
									File:                 hyperv1.WorkloadIdentity{ClientID: "client-6"},
									Network:              hyperv1.WorkloadIdentity{ClientID: "client-7"},
									ControlPlaneOperator: hyperv1.WorkloadIdentity{ClientID: "client-8"},
								},
							},
						},
					},
				},
			},
			expectNil:      false,
			expectStatus:   metav1.ConditionTrue,
			expectReason:   hyperv1.AzureWorkloadIdentityValidReason,
			expectContains: "All required",
		},
		{
			name: "WorkloadIdentities with all clientIDs returns valid",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 7},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
								WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
									CloudProvider:      hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
									NodePoolManagement: hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-ffffffffffff"},
									Ingress:            hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-111111111111"},
									ImageRegistry:      hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-222222222222"},
									Disk:               hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-333333333333"},
									File:               hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-444444444444"},
									Network:            hyperv1.WorkloadIdentity{ClientID: "aaaaaaaa-bbbb-cccc-dddd-555555555555"},
								},
							},
						},
					},
				},
			},
			expectNil:      false,
			expectStatus:   metav1.ConditionTrue,
			expectReason:   hyperv1.AzureWorkloadIdentityValidReason,
			expectContains: "All required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			condition := validateWorkloadIdentityConfig(tc.hcluster)

			if tc.expectNil {
				g.Expect(condition).To(BeNil())
				return
			}

			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Type).To(Equal(string(hyperv1.ValidAzureWorkloadIdentity)))
			g.Expect(condition.Status).To(Equal(tc.expectStatus))
			g.Expect(condition.Reason).To(Equal(tc.expectReason))
			g.Expect(condition.Message).To(ContainSubstring(tc.expectContains))
			g.Expect(condition.ObservedGeneration).To(Equal(tc.hcluster.Generation))
		})
	}
}
