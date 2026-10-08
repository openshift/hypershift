//go:build e2ev2

package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestAWSPlatformConfigDumpMachineDiagnostics(t *testing.T) {
	t.Run("When AWS diagnostics are requested, it should skip the Azure-only collection step", func(t *testing.T) {
		g := NewWithT(t)
		kubeconfig := filepath.Join(t.TempDir(), "missing-kubeconfig")
		t.Setenv("KUBECONFIG", kubeconfig)
		artifactDir := filepath.Join(t.TempDir(), "diagnostics")

		platform := &AWSPlatformConfig{}
		g.Expect(platform.DumpMachineDiagnostics(t.Context(), "clusters", "hc", artifactDir)).To(Succeed())
		_, err := os.Stat(artifactDir)
		g.Expect(os.IsNotExist(err)).To(BeTrue(), "AWS hook should not create Azure diagnostics artifacts")
	})
}
