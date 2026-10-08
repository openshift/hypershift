//go:build e2ev2

package lifecycle

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestAWSPlatformConfigDumpArgs(t *testing.T) {
	t.Run("When AWS dump args are requested, it should omit Azure credentials", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect((&AWSPlatformConfig{}).DumpArgs()).To(BeEmpty())
	})
}
