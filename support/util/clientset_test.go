package util

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func writeTestKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	kubeconfigFile := filepath.Join(dir, "kubeconfig")
	content := `apiVersion: v1
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
	err := os.WriteFile(kubeconfigFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	return kubeconfigFile
}

func TestGetKubeClientSet(t *testing.T) {
	t.Run("When KUBECONFIG env var points to a valid kubeconfig, it should create a clientset", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv("KUBECONFIG", writeTestKubeconfig(t))
		kc, err := GetKubeClientSet()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(kc).ToNot(BeNil())
	})
}

func TestGetKubeClientSetWithKubeconfig(t *testing.T) {
	tests := []struct {
		name             string
		kubeconfigPath   string
		useHelper        bool
		setKubeconfigEnv bool
		expectError      bool
		errorContains    string
	}{
		{
			name:             "When kubeconfig path is empty, it should fall back to KUBECONFIG env var resolution",
			kubeconfigPath:   "",
			setKubeconfigEnv: true,
		},
		{
			name:           "When kubeconfig file does not exist, it should return an error",
			kubeconfigPath: "/nonexistent/path/kubeconfig",
			expectError:    true,
			errorContains:  "unable to get kubernetes config",
		},
		{
			name:        "When a valid kubeconfig file is provided, it should create a clientset",
			useHelper:   true,
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			kubeconfigPath := tc.kubeconfigPath
			if tc.setKubeconfigEnv {
				t.Setenv("KUBECONFIG", writeTestKubeconfig(t))
			}
			if tc.useHelper {
				kubeconfigPath = writeTestKubeconfig(t)
			}

			kc, err := GetKubeClientSetWithKubeconfig(kubeconfigPath)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(kc).ToNot(BeNil())
			}
		})
	}
}
