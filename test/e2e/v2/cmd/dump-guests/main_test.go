//go:build e2ev2

package main

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestDumpClusterArgs(t *testing.T) {
	t.Run("When building generic dump arguments, it should omit Azure-specific credentials", func(t *testing.T) {
		g := NewWithT(t)
		args := dumpClusterArgs("/artifacts/hc", "hc", "clusters-hc")

		g.Expect(args).To(ConsistOf(
			"dump",
			"cluster",
			"--artifact-dir=/artifacts/hc",
			"--dump-guest-cluster=true",
			"--name=hc",
			"--namespace=clusters-hc",
		))
	})
}
