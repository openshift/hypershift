//go:build e2ev2

package lifecycle

import (
	"strings"
	"testing"
)

func TestAzurePlatformConfigTestMatrix(t *testing.T) {
	testCases := []struct {
		name                     string
		encryptionKeyID          string
		expectManagedHSMCoverage bool
	}{
		{
			name:                     "When the encryption key is in Managed HSM, it should select Managed HSM coverage",
			encryptionKeyID:          "https://test-hsm.managedhsm.azure.net/keys/test-key/version",
			expectManagedHSMCoverage: true,
		},
		{
			name:                     "When the encryption key is in Key Vault, it should not select Managed HSM coverage",
			encryptionKeyID:          "https://test-vault.vault.azure.net/keys/test-key/version",
			expectManagedHSMCoverage: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &AzurePlatformConfig{encryptionKeyID: tc.encryptionKeyID}
			matrix := cfg.TestMatrix("")

			if len(matrix.Parallel) == 0 {
				t.Fatal("expected Azure test matrix to contain a public test group")
			}
			hasManagedHSMCoverage := strings.Contains(matrix.Parallel[0].LabelFilter, "azure-managed-hsm")
			if hasManagedHSMCoverage != tc.expectManagedHSMCoverage {
				t.Fatalf("expected Managed HSM coverage selection to be %t, got %t in label filter %q",
					tc.expectManagedHSMCoverage, hasManagedHSMCoverage, matrix.Parallel[0].LabelFilter)
			}
		})
	}
}
