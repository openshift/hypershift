package imageprovider

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetImages(t *testing.T) {
	testComponents := []string{"component1", "component2"}

	testCases := []struct {
		name             string
		componentsImages map[string]string
		expectedImage    string
		missingImages    []string
	}{
		{
			name: "image found in the list",
			componentsImages: map[string]string{
				"component1": "image1",
				"component2": "image2",
				"component3": "image3",
			},
			expectedImage: "image1",
		},
		{
			name: "image found in the list with multiple options",
			componentsImages: map[string]string{
				"component1": "image1",
				"component2": "image2",
				"component3": "image3",
			},
			expectedImage: "image1",
		},
		{
			name: "image found in the list with multiple options but the first one is empty",
			componentsImages: map[string]string{
				"component2": "image2",
				"component3": "image3",
			},
			expectedImage: "image2",
		},
		{
			name: "image not found in the list",
			componentsImages: map[string]string{
				"component3": "image3",
			},
			expectedImage: "",
			missingImages: testComponents,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			releaseProvider := NewFromImages(tc.componentsImages)
			image := releaseProvider.GetImages(testComponents)
			g.Expect(image).To(Equal(tc.expectedImage))
			if tc.expectedImage == "" {
				g.Expect(releaseProvider.GetMissingImages()).To(ConsistOf(testComponents))
			}
		})
	}
}
