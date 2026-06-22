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

func TestGetConfigWithKubeconfig(t *testing.T) {
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
			errorContains:  "unable to build config from kubeconfig",
		},
		{
			name:        "When a valid kubeconfig file is provided, it should create a config with correct QPS and burst",
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

			cfg, err := GetConfigWithKubeconfig(kubeconfigPath)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cfg).ToNot(BeNil())
				g.Expect(cfg.QPS).To(Equal(float32(100)))
				g.Expect(cfg.Burst).To(Equal(100))
				g.Expect(cfg.Host).To(Equal("https://localhost:6443"))
			}
		})
	}
}

func TestGetClientWithKubeconfig(t *testing.T) {
	tests := []struct {
		name           string
		kubeconfigPath string
		useHelper      bool
		fakeClient     bool
		expectError    bool
		errorContains  string
	}{
		{
			name:        "When FAKE_CLIENT is true, it should return a fake client regardless of kubeconfig",
			fakeClient:  true,
			expectError: false,
		},
		{
			name:           "When kubeconfig file does not exist, it should return an error",
			kubeconfigPath: "/nonexistent/path/kubeconfig",
			expectError:    true,
			errorContains:  "unable to get kubernetes config",
		},
		{
			name:        "When a valid kubeconfig is provided, it should create a client",
			useHelper:   true,
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.fakeClient {
				t.Setenv("FAKE_CLIENT", "true")
			} else {
				t.Setenv("FAKE_CLIENT", "")
			}

			kubeconfigPath := tc.kubeconfigPath
			if tc.useHelper {
				kubeconfigPath = writeTestKubeconfig(t)
			}

			client, err := GetClientWithKubeconfig(kubeconfigPath)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(client).ToNot(BeNil())
			}
		})
	}
}
