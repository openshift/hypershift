package util

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func writeTestKubeconfig(t *testing.T) string {
	t.Helper()
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
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
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfigContent), 0600); err != nil {
		t.Fatalf("failed to write test kubeconfig: %v", err)
	}
	return kubeconfigPath
}

func TestGetConfigFromKubeconfig(t *testing.T) {
	t.Run("When kubeconfig is empty it should use KUBECONFIG env var for config resolution", func(t *testing.T) {
		g := NewGomegaWithT(t)
		kubeconfigPath := writeTestKubeconfig(t)
		t.Setenv("KUBECONFIG", kubeconfigPath)

		cfg, err := GetConfigFromKubeconfig("")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cfg).NotTo(BeNil())
		g.Expect(cfg.Host).To(Equal("https://localhost:6443"))
	})

	t.Run("When kubeconfig points to a non-existent file it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		_, err := GetConfigFromKubeconfig("/nonexistent/path/kubeconfig")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When kubeconfig points to a valid file it should return a config", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		kubeconfigPath := writeTestKubeconfig(t)

		cfg, err := GetConfigFromKubeconfig(kubeconfigPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cfg).NotTo(BeNil())
		g.Expect(cfg.Host).To(Equal("https://localhost:6443"))
		g.Expect(cfg.QPS).To(Equal(float32(100)))
		g.Expect(cfg.Burst).To(Equal(100))
	})

	t.Run("When kubeconfig points to a file with malformed content it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
		err := os.WriteFile(kubeconfigPath, []byte("not-valid-yaml: ["), 0600)
		g.Expect(err).NotTo(HaveOccurred())

		_, err = GetConfigFromKubeconfig(kubeconfigPath)
		g.Expect(err).To(HaveOccurred())
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
		kubeconfigPath := writeTestKubeconfig(t)

		client, err := GetClientFromKubeconfig(kubeconfigPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(client).NotTo(BeNil())
	})
}
