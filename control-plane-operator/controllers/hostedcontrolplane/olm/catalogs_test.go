package olm

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetCatalogToImageWithISImageRegistryOverrides(t *testing.T) {
	tests := []struct {
		name                     string
		catalogToImage           map[string]string
		isImageRegistryOverrides map[string][]string
		expected                 map[string]string
	}{
		{
			name: "No overrides",
			catalogToImage: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.16",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.16",
			},
			isImageRegistryOverrides: map[string][]string{},
			expected: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.16",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.16",
			},
		},
		{
			name: "Single override and different tag",
			catalogToImage: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.17",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.17",
			},
			isImageRegistryOverrides: map[string][]string{
				"registry.redhat.io": {"custom.registry.io"},
			},
			expected: map[string]string{
				"certified-operators": "custom.registry.io/redhat/certified-operator-index:v4.17",
				"community-operators": "custom.registry.io/redhat/community-operator-index:v4.17",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := getCatalogToImageWithISImageRegistryOverrides(tt.catalogToImage, tt.isImageRegistryOverrides)
			g.Expect(result).To(Equal(tt.expected), "Expected %d entries, but got %d", len(tt.expected), len(result))
			for key, expectedValue := range tt.expected {
				g.Expect(expectedValue).To(Equal(result[key]), "For key %s, expected %s, but got %s", key, expectedValue, result[key])
			}
		})
	}
}
