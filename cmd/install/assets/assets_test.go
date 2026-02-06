package assets

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestCapiResources(t *testing.T) {
	t.Run("When loading all capiResources paths they should exist and parse correctly", func(t *testing.T) {
		g := NewGomegaWithT(t)
		for path := range capiResources {
			// getCustomResourceDefinition panics if file doesn't exist or can't be parsed
			g.Expect(func() {
				crd := getCustomResourceDefinition(CRDS, path)
				g.Expect(crd).ToNot(BeNil())
			}).ToNot(Panic(), "path %s should exist and parse", path)
		}
	})

	t.Run("When loading a non-existent path it should panic", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(func() {
			getCustomResourceDefinition(CRDS, "cluster-api-provider-fake/nonexistent.yaml")
		}).To(Panic())
	})
}
