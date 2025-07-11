package secretproviderclass

import (
	"os"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func TestFormatSecretProviderClassObject(t *testing.T) {
	objectFormat := `
array:
  - |
    objectName: %s
    objectEncoding: %s
    objectType: secret
`
	testCases := []struct {
		name           string
		certName       string
		objectEncoding string
		expected       string
	}{
		{
			name:           "default",
			certName:       "cert",
			objectEncoding: "base64",
			expected: `
array:
  - |
    objectName: cert
    objectEncoding: base64
    objectType: secret
`,
		},
		{
			name:           "default",
			certName:       "cert",
			objectEncoding: "utf-8",
			expected: `
array:
  - |
    objectName: cert
    objectEncoding: utf-8
    objectType: secret
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			actual := formatSecretProviderClassObject(objectFormat, tc.certName, tc.objectEncoding)
			g.Expect(actual).To(Equal(tc.expected))
		})
	}
}

func TestReconcileManagedAzureSecretProviderClass(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{
					ManagedIdentities: hyperv1.AzureResourceManagedIdentities{
						ControlPlane: hyperv1.ControlPlaneManagedIdentities{
							ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
								Name:     "key-vault-name",
								TenantID: "tenant-id",
							},
						},
					},
				},
			},
		},
	}

	managedIdentity := hyperv1.ManagedIdentity{
		ClientID:              "client-id",
		CertificateName:       "certificate-name",
		CredentialsSecretName: "credentials-name",
		ObjectEncoding:        "utf-8",
	}

	testCases := []struct {
		name                string
		secretProviderClass *secretsstorev1.SecretProviderClass
		isMIv3              bool
		expected            *secretsstorev1.SecretProviderClass
	}{
		{
			name: "when isMIv3 is true, expect the objects field to contain the CredentialsSecretName value",
			secretProviderClass: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "user-assigned-identity-id",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "object-name:object-encoding",
					},
				},
			},
			isMIv3: true,
			expected: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "key-vault-user",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "\narray:\n  - |\n    objectName: credentials-name\n    objectEncoding: utf-8\n    objectType: secret\n",
					},
					SecretObjects: nil,
				},
			},
		},
		{
			name: "when isMIv3 is not passed in, expect the objects field to contain the CertificateName value",
			secretProviderClass: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "user-assigned-identity-id",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "object-name:object-encoding",
					},
				},
			},
			expected: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "key-vault-user",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "\narray:\n  - |\n    objectName: certificate-name\n    objectEncoding: utf-8\n    objectType: secret\n",
					},
					SecretObjects: nil,
				},
			},
		},
		{
			name: "when isMIv3 is false, expect the objects field to contain the CertificateName value",
			secretProviderClass: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "user-assigned-identity-id",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "object-name:object-encoding",
					},
				},
			},
			isMIv3: false,
			expected: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "key-vault-user",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "\narray:\n  - |\n    objectName: certificate-name\n    objectEncoding: utf-8\n    objectType: secret\n",
					},
					SecretObjects: nil,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			_ = os.Setenv("ARO_HCP_KEY_VAULT_USER_CLIENT_ID", "key-vault-user")
			if tc.name == "when isMIv3 is not passed in, expect the objects field to contain the CertificateName value" {
				ReconcileManagedAzureSecretProviderClass(tc.secretProviderClass, hcp, managedIdentity)
			} else {
				ReconcileManagedAzureSecretProviderClass(tc.secretProviderClass, hcp, managedIdentity, tc.isMIv3)
			}

			g.Expect(tc.secretProviderClass.Spec).To(Equal(tc.expected.Spec))
		})
	}
}

func TestReconcileAzureKMSClusterSeedSecretProviderClass(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Azure: &hyperv1.AzurePlatformSpec{
					ManagedIdentities: hyperv1.AzureResourceManagedIdentities{
						ControlPlane: hyperv1.ControlPlaneManagedIdentities{
							ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
								Name:     "key-vault-name",
								TenantID: "tenant-id",
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		name                  string
		secretProviderClass   *secretsstorev1.SecretProviderClass
		clusterSeedSecretName string
		expected              *secretsstorev1.SecretProviderClass
	}{
		{
			name: "cluster seed secret provider class is configured correctly",
			secretProviderClass: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "user-assigned-identity-id",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "object-name:object-encoding",
					},
				},
			},
			clusterSeedSecretName: "cluster-seed-secret",
			expected: &secretsstorev1.SecretProviderClass{
				Spec: secretsstorev1.SecretProviderClassSpec{
					Provider: "azure",
					Parameters: map[string]string{
						"usePodIdentity":         "false",
						"useVMManagedIdentity":   "true",
						"userAssignedIdentityID": "key-vault-user",
						"keyvaultName":           "key-vault-name",
						"tenantId":               "tenant-id",
						"objects":                "\narray:\n  - |\n    objectName: cluster-seed-secret\n    objectEncoding: base64\n    objectType: secret\n    objectAlias: cluster-seed\n",
					},
					SecretObjects: nil,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			_ = os.Setenv("ARO_HCP_KEY_VAULT_USER_CLIENT_ID", "key-vault-user")

			ReconcileAzureKMSClusterSeedSecretProviderClass(tc.secretProviderClass, hcp, tc.clusterSeedSecretName)

			g.Expect(tc.secretProviderClass.Spec).To(Equal(tc.expected.Spec))
		})
	}
}
