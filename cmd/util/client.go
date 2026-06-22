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

	KubeconfigFlagHelp = "Path to a kubeconfig file for the management cluster. If not specified, the default kubeconfig resolution is used (KUBECONFIG env var, then in-cluster config, then ~/.kube/config)."
)

// GetConfig creates a REST config from current context
func GetConfig() (*rest.Config, error) {
	return GetConfigWithKubeconfig("")
}

// GetConfigWithKubeconfig creates a REST config from the specified kubeconfig file path.
// If kubeconfigPath is empty, it falls back to the default kubeconfig resolution.
func GetConfigWithKubeconfig(kubeconfigPath string) (*rest.Config, error) {
	var cfg *rest.Config
	var err error
	if kubeconfigPath == "" {
		cfg, err = cr.GetConfig()
		if err != nil {
			return nil, err
		}
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("unable to build config from kubeconfig %q: %w", kubeconfigPath, err)
		}
	}
	cfg.QPS = 100
	cfg.Burst = 100
	return cfg, nil
}

// GetClient creates a controller-runtime client for Kubernetes
func GetClient() (crclient.Client, error) {
	return GetClientWithKubeconfig("")
}

// GetClientWithKubeconfig creates a controller-runtime client for Kubernetes using
// the specified kubeconfig file path. If kubeconfigPath is empty, it falls back to
// the default kubeconfig resolution.
func GetClientWithKubeconfig(kubeconfigPath string) (crclient.Client, error) {
	if os.Getenv("FAKE_CLIENT") == "true" {
		return fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(), nil
	}

	config, err := GetConfigWithKubeconfig(kubeconfigPath)
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
