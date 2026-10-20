//go:build e2ev2

package lifecycle

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestAWSPlatformConfigClusterSpecs(t *testing.T) {
	t.Run("When AWS cluster specs are built, it should omit Azure dump credentials for every variant", func(t *testing.T) {
		g := NewWithT(t)
		specs := (&AWSPlatformConfig{}).ClusterSpecs("release-image", "n1-image")
		g.Expect(specs).NotTo(BeEmpty())
		for _, spec := range specs {
			g.Expect(spec.DumpArgs).To(BeEmpty(), "variant %s", spec.Variant)
		}
	})
}
