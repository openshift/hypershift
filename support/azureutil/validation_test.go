package azureutil

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestValidateAzureResourceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceName string
		resourceType string
		expectErr    bool
	}{
		{
			name:         "When name is under 80 characters it should pass",
			resourceName: "pls-my-cluster",
			resourceType: "Private Link Service",
			expectErr:    false,
		},
		{
			name:         "When name is exactly 80 characters it should pass",
			resourceName: strings.Repeat("a", AzureResourceNameMaxLength),
			resourceType: "Private Endpoint",
			expectErr:    false,
		},
		{
			name:         "When name exceeds 80 characters it should return an error",
			resourceName: strings.Repeat("a", AzureResourceNameMaxLength+1),
			resourceType: "VNet Link",
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			err := ValidateAzureResourceName(tt.resourceName, tt.resourceType)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.resourceType))
				g.Expect(err.Error()).To(ContainSubstring("exceeds Azure maximum of 80 characters"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
