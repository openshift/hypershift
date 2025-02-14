package util

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/client-go/rest"
)

func TestRestConfigToKubeconfig(t *testing.T) {
	tests := []struct {
		name        string
		config      *rest.Config
		contextName string
		wantErr     bool
		setup       func(t *testing.T) func()
	}{
		{
			name: "basic config",
			config: &rest.Config{
				Host:        "https://api.example.com:6443",
				BearerToken: "test-token",
				TLSClientConfig: rest.TLSClientConfig{
					CAData:   []byte("test-ca-data"),
					Insecure: false,
				},
			},
			contextName: "test-context",
			wantErr:     false,
		},
		{
			name: "config with client cert/key",
			config: &rest.Config{
				Host: "https://api.example.com:6443",
				TLSClientConfig: rest.TLSClientConfig{
					CertData: []byte("test-cert-data"),
					KeyData:  []byte("test-key-data"),
					CAData:   []byte("test-ca-data"),
					Insecure: false,
				},
			},
			contextName: "test-context",
			wantErr:     false,
		},
		{
			name: "config with username/password",
			config: &rest.Config{
				Host:     "https://api.example.com:6443",
				Username: "test-user",
				Password: "test-password",
				TLSClientConfig: rest.TLSClientConfig{
					CAData:   []byte("test-ca-data"),
					Insecure: false,
				},
			},
			contextName: "test-context",
			wantErr:     false,
		},
		{
			name: "error case - no write permissions",
			config: &rest.Config{
				Host: "https://api.example.com:6443",
			},
			contextName: "test-context",
			wantErr:     true,
			setup: func(t *testing.T) func() {
				g := NewWithT(t)

				// Create a temporary directory with no write permissions
				tempDir, err := os.MkdirTemp("", "test-no-write-*")
				g.Expect(err).NotTo(HaveOccurred())

				// Create a subdirectory that we'll make read-only
				readOnlyDir := filepath.Join(tempDir, "readonly")
				err = os.Mkdir(readOnlyDir, 0755)
				g.Expect(err).NotTo(HaveOccurred())

				// Change permissions to read-only
				err = os.Chmod(readOnlyDir, fs.ModeDir|0500) // read + execute, no write
				g.Expect(err).NotTo(HaveOccurred())

				// Save original temp dir
				originalTempDir := os.TempDir()

				// Set temp dir to our read-only directory to force an error
				os.Setenv("TMPDIR", readOnlyDir)

				// Return cleanup function
				return func() {
					// Reset temp dir
					os.Setenv("TMPDIR", originalTempDir)
					// Make the directory writable again so we can remove it
					os.Chmod(readOnlyDir, 0755)
					os.RemoveAll(tempDir)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Run setup if provided
			var cleanup func()
			if tt.setup != nil {
				cleanup = tt.setup(t)
				defer cleanup()
			}

			// Call the function
			kubeconfigPath, err := RestConfigToKubeconfig(tt.config, tt.contextName)

			// Check error
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), "unexpected error creating kubeconfig")

			// Verify file exists and clean up after test
			defer os.Remove(kubeconfigPath)
			_, err = os.Stat(kubeconfigPath)
			g.Expect(err).NotTo(HaveOccurred(), "kubeconfig file should exist")

			// Read the kubeconfig file
			kubeconfigBytes, err := os.ReadFile(kubeconfigPath)
			g.Expect(err).NotTo(HaveOccurred(), "should be able to read kubeconfig file")
			kubeconfigContent := string(kubeconfigBytes)

			// Just verify that the file is not empty
			g.Expect(len(kubeconfigBytes)).To(BeNumerically(">", 0), "kubeconfig file should not be empty")

			// Log the content for debugging
			t.Logf("Generated kubeconfig content: %s", kubeconfigContent)

			// Basic content checks
			g.Expect(kubeconfigContent).To(ContainSubstring("apiVersion: v1"), "should contain apiVersion: v1")
			g.Expect(kubeconfigContent).To(ContainSubstring("kind: Config"), "should contain kind: Config")
			g.Expect(kubeconfigContent).To(ContainSubstring(fmt.Sprintf("current-context: %s", tt.contextName)), "should contain correct current-context")
			g.Expect(kubeconfigContent).To(ContainSubstring(tt.config.Host), "should contain server URL")

			// Check for specific auth info based on the config type using switch with len()
			switch {
			case len(tt.config.BearerToken) > 0:
				g.Expect(kubeconfigContent).To(ContainSubstring(fmt.Sprintf("token: %s", tt.config.BearerToken)), "should contain token")
			case len(tt.config.CertData) > 0 && len(tt.config.KeyData) > 0:
				g.Expect(kubeconfigContent).To(ContainSubstring("client-certificate-data:"), "should contain client cert data")
				g.Expect(kubeconfigContent).To(ContainSubstring("client-key-data:"), "should contain client key data")
			case len(tt.config.Username) > 0 && len(tt.config.Password) > 0:
				g.Expect(kubeconfigContent).To(ContainSubstring(fmt.Sprintf("username: %s", tt.config.Username)), "should contain username")
				g.Expect(kubeconfigContent).To(ContainSubstring(fmt.Sprintf("password: %s", tt.config.Password)), "should contain password")
			}
		})
	}
}
