package olm

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetCatalogToImagesWithVersion(t *testing.T) {
	tests := []struct {
		name           string
		releaseVersion string
		expectedResult map[string]string
		expectedError  bool
	}{
		{
			name:           "basic case",
			releaseVersion: "4.8.0",
			expectedResult: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.8",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.8",
				"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.8",
				"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.8",
			},
			expectedError: false,
		},
		{
			name:           "different version",
			releaseVersion: "4.9.1",
			expectedResult: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.9",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.9",
				"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.9",
				"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.9",
			},
			expectedError: false,
		},
		{
			name:           "empty releaseVersion",
			releaseVersion: "",
			expectedResult: nil,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result, err := GetCatalogToImagesWithVersion(tt.releaseVersion)
			g.Expect(tt.expectedError).To(Equal(err != nil))
			g.Expect(tt.expectedResult).To(Equal(result))
		})
	}
}
