package kms

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

func TestBuildKASContainerAzureKMS(t *testing.T) {
	tests := []struct {
		name             string
		keyVaultType     hyperv1.AzureKMSKeyVaultType
		expectManagedHSM bool
	}{
		{
			name: "When keyVaultType is omitted, it should not pass the managed HSM flag",
		},
		{
			name:         "When keyVaultType is KeyVault, it should not pass the managed HSM flag",
			keyVaultType: hyperv1.AzureKMSKeyVaultTypeKeyVault,
		},
		{
			name:             "When keyVaultType is ManagedHSM, it should pass the managed HSM flag",
			keyVaultType:     hyperv1.AzureKMSKeyVaultTypeManagedHSM,
			expectManagedHSM: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			spec := &hyperv1.AzureKMSSpec{
				ActiveKey: hyperv1.AzureKMSKey{
					KeyVaultName: "vault",
					KeyName:      "key",
					KeyVersion:   "1",
				},
				BackupKey: &hyperv1.AzureKMSKey{ //nolint:staticcheck
					KeyVaultName: "vault",
					KeyName:      "backup",
					KeyVersion:   "1",
				},
				KeyVaultType: tt.keyVaultType,
			}
			provider, err := NewAzureKMSProvider(spec.ActiveKey, spec.BackupKey, spec, "test-kms-image:latest") //nolint:staticcheck
			g.Expect(err).NotTo(HaveOccurred())

			podConfig, err := provider.GenerateKMSPodConfig()
			g.Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{"azure-kms-provider-active", "azure-kms-provider-backup"} {
				container := findContainer(podConfig.Containers, name)
				g.Expect(container).NotTo(BeNil())
				if tt.expectManagedHSM {
					g.Expect(container.Args).To(ContainElement("--managed-hsm"))
				} else {
					g.Expect(container.Args).NotTo(ContainElement("--managed-hsm"))
				}
			}
		})
	}
}

func TestAzureKMSProviderName(t *testing.T) {
	const legacyProviderName = "azure-be23a676"
	key := hyperv1.AzureKMSKey{KeyVaultName: "vault", KeyName: "key", KeyVersion: "1"}

	tests := []struct {
		name         string
		keyVaultType hyperv1.AzureKMSKeyVaultType
		expectLegacy bool
	}{
		{
			name:         "When keyVaultType is omitted, it should produce the legacy provider name",
			expectLegacy: true,
		},
		{
			name:         "When keyVaultType is KeyVault, it should preserve the legacy provider name",
			keyVaultType: hyperv1.AzureKMSKeyVaultTypeKeyVault,
			expectLegacy: true,
		},
		{
			name:         "When keyVaultType is ManagedHSM, it should produce a different provider name",
			keyVaultType: hyperv1.AzureKMSKeyVaultTypeManagedHSM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			name, err := AzureKMSProviderName(key, tt.keyVaultType)
			g.Expect(err).NotTo(HaveOccurred())
			if tt.expectLegacy {
				g.Expect(name).To(Equal(legacyProviderName))
			} else {
				g.Expect(name).NotTo(Equal(legacyProviderName))
			}
		})
	}
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}
