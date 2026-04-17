package azure

import (
	"os"
	"path/filepath"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/util"
)

func TestApplyPlatformSpecificsWhenWorkloadIdentitiesFileIsProvidedItShouldPreferWorkloadAuthOverStaleManagedIdentities(t *testing.T) {
	td := t.TempDir()
	wiPath := filepath.Join(td, "workload-identities.json")
	wiJSON := []byte(`{
  "cloudProvider":{"clientID":"11111111-1111-1111-1111-111111111111"},
  "nodePoolManagement":{"clientID":"22222222-2222-2222-2222-222222222222"},
  "ingress":{"clientID":"33333333-3333-3333-3333-333333333333"},
  "network":{"clientID":"44444444-4444-4444-4444-444444444444"},
  "imageRegistry":{"clientID":"55555555-5555-5555-5555-555555555555"},
  "disk":{"clientID":"66666666-6666-6666-6666-666666666666"},
  "file":{"clientID":"77777777-7777-7777-7777-777777777777"},
  "controlPlaneOperator":{"clientID":"88888888-8888-8888-8888-888888888888"}
}`)
	if err := os.WriteFile(wiPath, wiJSON, 0600); err != nil {
		t.Fatal(err)
	}

	opts := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: &ValidatedCreateOptions{
				validatedCreateOptions: &validatedCreateOptions{
					RawCreateOptions: &RawCreateOptions{
						CredentialsFile:        filepath.Join(td, "unused-creds.yaml"),
						WorkloadIdentitiesFile: wiPath,
					},
				},
			},
			name:      "example",
			namespace: "clusters",
			infra: &azureinfra.CreateInfraOutput{
				BaseDomain:         "example.com",
				PublicZoneID:       "/public",
				PrivateZoneID:      "/private",
				Location:           "eastus",
				ResourceGroupName:  "rg",
				VNetID:             "/vnet",
				SubnetID:           "/subnet",
				SecurityGroupID:    "/nsg",
				InfraID:            "infra-id",
				ControlPlaneMIs:    &hyperv1.AzureResourceManagedIdentities{},
				WorkloadIdentities: nil,
			},
			creds: util.AzureCreds{
				SubscriptionID: "sub",
				TenantID:       "tenant",
				ClientID:       "client",
				ClientSecret:   "secret",
			},
		},
	}

	hc := &hyperv1.HostedCluster{}
	if err := opts.ApplyPlatformSpecifics(hc); err != nil {
		t.Fatal(err)
	}

	auth := hc.Spec.Platform.Azure.AzureAuthenticationConfig
	if auth.AzureAuthenticationConfigType != hyperv1.AzureAuthenticationTypeWorkloadIdentities {
		t.Fatalf("expected WorkloadIdentities auth, got %q", auth.AzureAuthenticationConfigType)
	}
	if auth.WorkloadIdentities == nil || auth.WorkloadIdentities.CloudProvider.ClientID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("expected workload identities from file, got %#v", auth.WorkloadIdentities)
	}
}

func TestApplyPlatformSpecificsObjectEncodingOnlySetOnValidManagedIdentities(t *testing.T) {
	tests := []struct {
		name                    string
		controlPlaneMIs         *hyperv1.ControlPlaneManagedIdentities
		expectedObjectEncodings map[string]bool // true if objectEncoding should be set
	}{
		{
			name: "partial managed identities should only set objectEncoding on valid components",
			controlPlaneMIs: &hyperv1.ControlPlaneManagedIdentities{
				ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
					Name:     "keyvault",
					TenantID: "tenant",
				},
				CloudProvider: hyperv1.ManagedIdentity{
					ClientID:              "11111111-1111-1111-1111-111111111111",
					CredentialsSecretName: "cloud-secret",
				},
				NodePoolManagement: hyperv1.ManagedIdentity{
					ClientID:              "22222222-2222-2222-2222-222222222222",
					CredentialsSecretName: "nodepool-secret",
				},
				// Leave ControlPlaneOperator empty (no CredentialsSecretName)
				ControlPlaneOperator: hyperv1.ManagedIdentity{
					ClientID: "33333333-3333-3333-3333-333333333333",
					// CredentialsSecretName: "", // intentionally empty
				},
				// Completely omit ImageRegistry, Ingress, Network, Disk, File
			},
			expectedObjectEncodings: map[string]bool{
				"CloudProvider":        true,  // has CredentialsSecretName
				"NodePoolManagement":   true,  // has CredentialsSecretName
				"ControlPlaneOperator": false, // missing CredentialsSecretName
				"ImageRegistry":        false, // zero value
				"Ingress":              false, // zero value
				"Network":              false, // zero value
				"Disk":                 false, // zero value
				"File":                 false, // zero value
			},
		},
		{
			name: "complete managed identities should set objectEncoding on all components",
			controlPlaneMIs: &hyperv1.ControlPlaneManagedIdentities{
				ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
					Name:     "keyvault",
					TenantID: "tenant",
				},
				CloudProvider: hyperv1.ManagedIdentity{
					ClientID:              "11111111-1111-1111-1111-111111111111",
					CredentialsSecretName: "cloud-secret",
				},
				NodePoolManagement: hyperv1.ManagedIdentity{
					ClientID:              "22222222-2222-2222-2222-222222222222",
					CredentialsSecretName: "nodepool-secret",
				},
				ControlPlaneOperator: hyperv1.ManagedIdentity{
					ClientID:              "33333333-3333-3333-3333-333333333333",
					CredentialsSecretName: "cpo-secret",
				},
				ImageRegistry: hyperv1.ManagedIdentity{
					ClientID:              "44444444-4444-4444-4444-444444444444",
					CredentialsSecretName: "image-secret",
				},
				Ingress: hyperv1.ManagedIdentity{
					ClientID:              "55555555-5555-5555-5555-555555555555",
					CredentialsSecretName: "ingress-secret",
				},
				Network: hyperv1.ManagedIdentity{
					ClientID:              "66666666-6666-6666-6666-666666666666",
					CredentialsSecretName: "network-secret",
				},
				Disk: hyperv1.ManagedIdentity{
					ClientID:              "77777777-7777-7777-7777-777777777777",
					CredentialsSecretName: "disk-secret",
				},
				File: hyperv1.ManagedIdentity{
					ClientID:              "88888888-8888-8888-8888-888888888888",
					CredentialsSecretName: "file-secret",
				},
			},
			expectedObjectEncodings: map[string]bool{
				"CloudProvider":        true,
				"NodePoolManagement":   true,
				"ControlPlaneOperator": true,
				"ImageRegistry":        true,
				"Ingress":              true,
				"Network":              true,
				"Disk":                 true,
				"File":                 true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := &CreateOptions{
				completedCreateOptions: &completedCreateOptions{
					ValidatedCreateOptions: &ValidatedCreateOptions{
						validatedCreateOptions: &validatedCreateOptions{
							RawCreateOptions: &RawCreateOptions{
								CredentialsFile: "unused-creds.yaml",
							},
						},
					},
					name:      "example",
					namespace: "clusters",
					infra: &azureinfra.CreateInfraOutput{
						BaseDomain:        "example.com",
						PublicZoneID:      "/public",
						PrivateZoneID:     "/private",
						Location:          "eastus",
						ResourceGroupName: "rg",
						VNetID:            "/vnet",
						SubnetID:          "/subnet",
						SecurityGroupID:   "/nsg",
						InfraID:           "infra-id",
						ControlPlaneMIs: &hyperv1.AzureResourceManagedIdentities{
							ControlPlane: *tc.controlPlaneMIs,
						},
						WorkloadIdentities: nil,
					},
					creds: util.AzureCreds{
						SubscriptionID: "sub",
						TenantID:       "tenant",
						ClientID:       "client",
						ClientSecret:   "secret",
					},
				},
			}

			hc := &hyperv1.HostedCluster{}
			if err := opts.ApplyPlatformSpecifics(hc); err != nil {
				t.Fatal(err)
			}

			auth := hc.Spec.Platform.Azure.AzureAuthenticationConfig
			if auth.AzureAuthenticationConfigType != hyperv1.AzureAuthenticationTypeManagedIdentities {
				t.Fatalf("expected ManagedIdentities auth, got %q", auth.AzureAuthenticationConfigType)
			}

			// Verify objectEncoding is only set where expected
			cp := auth.ManagedIdentities.ControlPlane

			checkObjectEncoding := func(name string, mi hyperv1.ManagedIdentity, shouldHaveEncoding bool) {
				hasEncoding := string(mi.ObjectEncoding) == ObjectEncoding
				if shouldHaveEncoding && !hasEncoding {
					t.Errorf("%s: expected ObjectEncoding to be %q, but got %q", name, ObjectEncoding, mi.ObjectEncoding)
				}
				if !shouldHaveEncoding && hasEncoding {
					t.Errorf("%s: expected ObjectEncoding to be empty, but got %q", name, mi.ObjectEncoding)
				}
			}

			checkObjectEncoding("CloudProvider", cp.CloudProvider, tc.expectedObjectEncodings["CloudProvider"])
			checkObjectEncoding("NodePoolManagement", cp.NodePoolManagement, tc.expectedObjectEncodings["NodePoolManagement"])
			checkObjectEncoding("ControlPlaneOperator", cp.ControlPlaneOperator, tc.expectedObjectEncodings["ControlPlaneOperator"])
			checkObjectEncoding("ImageRegistry", cp.ImageRegistry, tc.expectedObjectEncodings["ImageRegistry"])
			checkObjectEncoding("Ingress", cp.Ingress, tc.expectedObjectEncodings["Ingress"])
			checkObjectEncoding("Network", cp.Network, tc.expectedObjectEncodings["Network"])
			checkObjectEncoding("Disk", cp.Disk, tc.expectedObjectEncodings["Disk"])
			checkObjectEncoding("File", cp.File, tc.expectedObjectEncodings["File"])
		})
	}
}
