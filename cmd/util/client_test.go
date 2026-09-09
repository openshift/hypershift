package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/client-go/kubernetes"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestGetConfig(t *testing.T) {
	t.Run("When KUBECONFIG env var points to a valid kubeconfig, it should create a config", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv("KUBECONFIG", writeTestKubeconfig(t))
		cfg, err := GetConfig()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cfg).ToNot(BeNil())
		g.Expect(cfg.QPS).To(Equal(float32(100)))
		g.Expect(cfg.Burst).To(Equal(100))
	})
}

func TestControllerRuntimeClientFor(t *testing.T) {
	g := NewWithT(t)
	controllerClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
	provider := &ClientProvider{
		ControllerRuntimeClient: func(_ string) (client.Client, error) {
			return controllerClient, nil
		},
	}

	got, err := provider.ControllerRuntimeClientFor("")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(controllerClient))
}

func TestClientProviderErrors(t *testing.T) {
	t.Run("When controller-runtime client provider is missing, it should return an error", func(t *testing.T) {
		_, err := (&ClientProvider{}).ControllerRuntimeClientFor("")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
	t.Run("When controller-runtime client factory returns an error, it should propagate it", func(t *testing.T) {
		_, err := (&ClientProvider{
			ControllerRuntimeClient: func(string) (client.Client, error) {
				return nil, errors.New("client factory failed")
			},
		}).ControllerRuntimeClientFor("")
		NewWithT(t).Expect(err).To(MatchError("client factory failed"))
	})
	t.Run("When controller-runtime client factory returns nil, it should return an error", func(t *testing.T) {
		_, err := (&ClientProvider{
			ControllerRuntimeClient: func(string) (client.Client, error) {
				return nil, nil
			},
		}).ControllerRuntimeClientFor("")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})

	t.Run("When typed client provider is missing, it should return an error", func(t *testing.T) {
		_, err := (&ClientProvider{}).KubernetesClientSetFor("")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
	t.Run("When REST config provider is missing, it should return an error", func(t *testing.T) {
		_, err := (&ClientProvider{}).ConfigFor("")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
	t.Run("When impersonated client provider is missing, it should return an error", func(t *testing.T) {
		_, err := (&ClientProvider{}).ImpersonatedClientFor("test-user")
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
}

func TestKubernetesClientSetFor(t *testing.T) {
	g := NewWithT(t)
	typedClient := fakekubeclient.NewClientset()
	provider := &ClientProvider{
		KubernetesClientSet: func(_ string) (kubernetes.Interface, error) {
			return typedClient, nil
		},
	}

	got, err := provider.KubernetesClientSetFor("")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(typedClient))
}

func TestConfigFor(t *testing.T) {
	g := NewWithT(t)
	config := &rest.Config{Host: "https://example.com"}
	provider := &ClientProvider{
		Config: func(_ string) (*rest.Config, error) {
			return config, nil
		},
	}

	got, err := provider.ConfigFor("")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(config))
}

func TestImpersonatedClientFor(t *testing.T) {
	g := NewWithT(t)
	controllerClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
	provider := &ClientProvider{
		ImpersonatedClient: func(_ string) (client.Client, error) {
			return controllerClient, nil
		},
	}

	got, err := provider.ImpersonatedClientFor("test-user")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal(controllerClient))
}

func TestResolveClientProvider(t *testing.T) {
	g := NewWithT(t)
	injected := &ClientProvider{}

	g.Expect(ResolveClientProvider(injected)).To(BeIdenticalTo(injected))
	g.Expect(ResolveClientProvider(nil)).NotTo(BeNil())
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
		expectError    bool
		errorContains  string
	}{
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

func TestGetKubernetesClientSetWithKubeconfig(t *testing.T) {
	g := NewWithT(t)
	client, err := GetKubernetesClientSetWithKubeconfig(writeTestKubeconfig(t))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(client).NotTo(BeNil())
}
