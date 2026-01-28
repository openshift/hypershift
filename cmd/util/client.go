package util

import (
	"fmt"
	"os"
	"strings"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	AutoInfraLabelName = "hypershift.openshift.io/auto-created-for-infra"
	// DeleteWithClusterLabelName marks CLI created secrets, to be safely removed on hosted cluster deletion
	DeleteWithClusterLabelName = "hypershift.openshift.io/safe-to-delete-with-cluster"
)

var (
	// kubeconfigPath stores the path to a custom kubeconfig file if specified via --kubeconfig flag
	kubeconfigPath string
)

// SetKubeconfig sets the path to a custom kubeconfig file.
// This should be called before GetConfig() or GetClient() to use a non-default kubeconfig.
// When path is empty (no --kubeconfig flag), the default kubeconfig resolution
// via controller-runtime's GetConfig() is used, which already handles the
// KUBECONFIG environment variable including colon-separated paths.
func SetKubeconfig(path string) error {
	if path == "" {
		return nil
	}

	// Validate that the file exists
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("kubeconfig file not found at %s: %w", path, err)
		}
		return fmt.Errorf("unable to access kubeconfig file: %w", err)
	}

	kubeconfigPath = path
	return nil
}

// GetConfig creates a REST config from current context or from the kubeconfig path set via SetKubeconfig
func GetConfig() (*rest.Config, error) {
	var cfg *rest.Config
	var err error

	if kubeconfigPath != "" {
		// Use custom kubeconfig path if set
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("unable to build config from kubeconfig %s: %w", kubeconfigPath, err)
		}
	} else {
		// Use default kubeconfig resolution
		cfg, err = cr.GetConfig()
		if err != nil {
			return nil, err
		}
	}

	cfg.QPS = 100
	cfg.Burst = 100
	return cfg, nil
}

// GetClient creates a controller-runtime client for Kubernetes
func GetClient() (crclient.Client, error) {
	if os.Getenv("FAKE_CLIENT") == "true" {
		return fake.NewFakeClient(), nil
	}

	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}

	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}

	return client, nil
}

// GetImpersonatedClient creates a controller-runtime client for Kubernetes
func GetImpersonatedClient(userName string) (crclient.Client, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	config.Impersonate = rest.ImpersonationConfig{
		UserName: userName,
	}

	client, err := crclient.New(config, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	return client, nil
}

// ParseAWSTags does exactly that
func ParseAWSTags(tags []string) (map[string]string, error) {
	tagMap := make(map[string]string, len(tags))
	for _, tagStr := range tags {
		parts := strings.SplitN(tagStr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid tag specification: %q (expecting \"key=value\")", tagStr)
		}
		tagMap[parts[0]] = parts[1]
	}
	return tagMap, nil
}
