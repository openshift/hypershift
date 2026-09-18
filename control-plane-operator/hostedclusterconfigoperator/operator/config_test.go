package operator

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestCfgFromFile(t *testing.T) {
	const validKubeconfig = `apiVersion: v1
clusters:
- cluster:
    server: https://infra.example.com
  name: infra
contexts:
- context:
    cluster: infra
    user: infra
  name: infra
current-context: infra
kind: Config
preferences: {}
users:
- name: infra
  user:
    token: test-token
`

	t.Run("When the kubeconfig file is valid, it should return the REST config", func(t *testing.T) {
		gt := NewWithT(t)
		path := filepath.Join(t.TempDir(), "kubeconfig")
		gt.Expect(os.WriteFile(path, []byte(validKubeconfig), 0o600)).To(Succeed())

		cfg, err := CfgFromFile(path)
		gt.Expect(err).NotTo(HaveOccurred())
		gt.Expect(cfg.Host).To(Equal("https://infra.example.com"))
	})

	t.Run("When the kubeconfig file does not exist, it should return a contextual error", func(t *testing.T) {
		gt := NewWithT(t)
		path := filepath.Join(t.TempDir(), "missing-kubeconfig")

		_, err := CfgFromFile(path)
		gt.Expect(err).To(MatchError(ContainSubstring("failed to load kubeconfig from path \"" + path + "\"")))
	})

	t.Run("When the kubeconfig file is invalid, it should return a contextual error", func(t *testing.T) {
		gt := NewWithT(t)
		path := filepath.Join(t.TempDir(), "invalid-kubeconfig")
		gt.Expect(os.WriteFile(path, []byte("not: [valid"), 0o600)).To(Succeed())

		_, err := CfgFromFile(path)
		gt.Expect(err).To(MatchError(ContainSubstring("failed to load kubeconfig from path \"" + path + "\"")))
	})
}
