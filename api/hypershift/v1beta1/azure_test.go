package v1beta1

import (
	"encoding/json"
	"testing"
)

type azureKMSKeyNMinus1 struct {
	// keyVaultName is the name of the Key Vault.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	KeyVaultName string `json:"keyVaultName,omitempty"`
	// keyName is the name of the key.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	KeyName string `json:"keyName,omitempty"`
	// keyVersion is the key version.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	KeyVersion string `json:"keyVersion,omitempty"`
}

func TestAzureKMSKeySerializationCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		current AzureKMSKey
	}{
		{
			name: "When keyVaultType is omitted it should remain compatible with the previous API",
			current: AzureKMSKey{
				KeyVaultName: "test-vault",
				KeyName:      "test-key",
				KeyVersion:   "1",
			},
		},
		{
			name: "When keyVaultType is ManagedHSM it should be ignored by the previous API",
			current: AzureKMSKey{
				KeyVaultName: "test-hsm",
				KeyName:      "test-key",
				KeyVersion:   "1",
				KeyVaultType: AzureKMSKeyVaultTypeManagedHSM,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current AzureKMSKey: %v", err)
			}

			var previous azureKMSKeyNMinus1
			if err := json.Unmarshal(data, &previous); err != nil {
				t.Fatalf("previous API failed to unmarshal current AzureKMSKey: %v", err)
			}
			if previous.KeyVaultName != tt.current.KeyVaultName ||
				previous.KeyName != tt.current.KeyName ||
				previous.KeyVersion != tt.current.KeyVersion {
				t.Fatalf("previous API key fields differ: got %+v, want %+v", previous, tt.current)
			}

			previousData, err := json.Marshal(previous)
			if err != nil {
				t.Fatalf("failed to marshal previous AzureKMSKey: %v", err)
			}
			var current AzureKMSKey
			if err := json.Unmarshal(previousData, &current); err != nil {
				t.Fatalf("current API failed to unmarshal previous AzureKMSKey: %v", err)
			}
			if current.KeyVaultType != "" {
				t.Fatalf("expected keyVaultType to be omitted after previous API round trip, got %q", current.KeyVaultType)
			}
		})
	}
}
