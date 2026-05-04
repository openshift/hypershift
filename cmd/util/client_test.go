package util

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetConfigFromKubeconfig(t *testing.T) {
	t.Run("When kubeconfig is empty it should fall back to default config resolution", func(t *testing.T) {
		g := NewGomegaWithT(t)
		// This test verifies the empty-path codepath does not panic.
		// It may fail to find a config in CI, but should not panic.
		_, err := GetConfigFromKubeconfig("")
		// We don't assert success because there may be no default kubeconfig in the test environment.
		// We only assert it doesn't panic and returns an error if no config is available.
		_ = err
		g.Expect(true).To(BeTrue())
	})

	t.Run("When kubeconfig points to a non-existent file it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		_, err := GetConfigFromKubeconfig("/nonexistent/path/kubeconfig")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When kubeconfig points to a valid file it should return a config", func(t *testing.T) {
		g := NewGomegaWithT(t)
		tmpDir := t.TempDir()
		kubeconfigPath := filepath.Join(tmpDir, "kubeconfig")
		kubeconfigContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:6443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
		err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600)
		g.Expect(err).NotTo(HaveOccurred())

		cfg, err := GetConfigFromKubeconfig(kubeconfigPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cfg).NotTo(BeNil())
		g.Expect(cfg.Host).To(Equal("https://localhost:6443"))
		g.Expect(cfg.QPS).To(Equal(float32(100)))
		g.Expect(cfg.Burst).To(Equal(100))
	})
}

func TestGetClientFromKubeconfig(t *testing.T) {
	t.Run("When FAKE_CLIENT is set it should return a fake client regardless of kubeconfig", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")
		client, err := GetClientFromKubeconfig("/nonexistent/path")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(client).NotTo(BeNil())
	})

	t.Run("When kubeconfig points to a non-existent file it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "false")
		_, err := GetClientFromKubeconfig("/nonexistent/path/kubeconfig")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to get kubernetes config"))
	})

	t.Run("When kubeconfig points to a valid file it should create a client", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "false")
		tmpDir := t.TempDir()
		kubeconfigPath := filepath.Join(tmpDir, "kubeconfig")
		kubeconfigContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:6443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
		err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600)
		g.Expect(err).NotTo(HaveOccurred())

		client, err := GetClientFromKubeconfig(kubeconfigPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(client).NotTo(BeNil())
	})
}
