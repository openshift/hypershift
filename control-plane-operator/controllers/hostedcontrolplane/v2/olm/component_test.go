package olm

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewComponents(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		capabilityImageStream bool
		expectedCount         int
	}{
		{
			name:                  "When capabilityImageStream is false, it should return 8 components",
			capabilityImageStream: false,
			expectedCount:         8,
		},
		{
			name:                  "When capabilityImageStream is true, it should return 8 components",
			capabilityImageStream: true,
			expectedCount:         8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			components := NewComponents(tc.capabilityImageStream)

			g.Expect(components).To(HaveLen(tc.expectedCount))
			for _, comp := range components {
				g.Expect(comp).ToNot(BeNil())
			}
		})
	}
}
