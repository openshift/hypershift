package util

import (
	"fmt"
	"strings"

	hyperapi "github.com/openshift/hypershift/support/api"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AutoInfraLabelName = "hypershift.openshift.io/auto-created-for-infra"
	// DeleteWithClusterLabelName marks CLI created secrets, to be safely removed on hosted cluster deletion
	DeleteWithClusterLabelName = "hypershift.openshift.io/safe-to-delete-with-cluster"

	KubeconfigFlagHelp = "Path to a kubeconfig file for the management cluster. If not specified, the default kubeconfig resolution is used (KUBECONFIG env var, then in-cluster config, then ~/.kube/config)."
)

// ClientFactory creates a controller-runtime client for the requested kubeconfig.
type ClientFactory func(kubeconfigPath string) (crclient.Client, error)

// KubeClientSetFactory creates a typed Kubernetes clientset for the requested kubeconfig.
type KubeClientSetFactory func(kubeconfigPath string) (kubernetes.Interface, error)

// ConfigFactory creates a REST config for the requested kubeconfig.
type ConfigFactory func(kubeconfigPath string) (*rest.Config, error)

// ImpersonatedClientFactory creates a controller-runtime client for an impersonated user.
type ImpersonatedClientFactory func(userName string) (crclient.Client, error)

// ClientProvider groups the management-cluster dependencies used by CLI commands.
// Tests can replace individual factories with fake clients without changing the
// command implementation.
type ClientProvider struct {
	ControllerRuntimeClient ClientFactory
	KubernetesClientSet     KubeClientSetFactory
	Config                  ConfigFactory
	ImpersonatedClient      ImpersonatedClientFactory
}

// DefaultClientProvider returns the production client factories used by the CLI.
func DefaultClientProvider() *ClientProvider {
	return &ClientProvider{
		ControllerRuntimeClient: GetClientWithKubeconfig,
		KubernetesClientSet:     GetKubernetesClientSetWithKubeconfig,
		Config:                  GetConfigWithKubeconfig,
		ImpersonatedClient:      GetImpersonatedClient,
	}
}

// ResolveClientProvider returns the supplied provider or the production
// provider when no dependency was injected.
func ResolveClientProvider(clientProviders ...*ClientProvider) *ClientProvider {
	if len(clientProviders) > 0 && clientProviders[0] != nil {
		return clientProviders[0]
	}
	return DefaultClientProvider()
}

// ControllerRuntimeClientFor returns a controller-runtime client from the
// provider and rejects missing or nil dependencies.
func (p *ClientProvider) ControllerRuntimeClientFor(kubeconfigPath string) (crclient.Client, error) {
	if p == nil || p.ControllerRuntimeClient == nil {
		return nil, fmt.Errorf("controller-runtime client provider is not configured")
	}
	client, err := p.ControllerRuntimeClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("controller-runtime client provider returned a nil client")
	}
	return client, nil
}

// KubernetesClientSetFor returns a typed Kubernetes clientset from the
// provider and rejects missing or nil dependencies.
func (p *ClientProvider) KubernetesClientSetFor(kubeconfigPath string) (kubernetes.Interface, error) {
	if p == nil || p.KubernetesClientSet == nil {
		return nil, fmt.Errorf("typed Kubernetes client provider is not configured")
	}
	client, err := p.KubernetesClientSet(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("typed Kubernetes client provider returned a nil client")
	}
	return client, nil
}

// ConfigFor returns a REST config from the provider and rejects missing or
// nil dependencies.
func (p *ClientProvider) ConfigFor(kubeconfigPath string) (*rest.Config, error) {
	if p == nil || p.Config == nil {
		return nil, fmt.Errorf("REST config provider is not configured")
	}
	config, err := p.Config(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("REST config provider returned a nil config")
	}
	return config, nil
}

// ImpersonatedClientFor returns an impersonated client from the provider and
// rejects missing or nil dependencies.
func (p *ClientProvider) ImpersonatedClientFor(userName string) (crclient.Client, error) {
	if p == nil || p.ImpersonatedClient == nil {
		return nil, fmt.Errorf("impersonated client provider is not configured")
	}
	client, err := p.ImpersonatedClient(userName)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("impersonated client provider returned a nil client")
	}
	return client, nil
}

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

// GetClientWithKubeconfig creates a controller-runtime client for Kubernetes
// using the specified kubeconfig path, or the default resolution when empty.
func GetClientWithKubeconfig(kubeconfigPath string) (crclient.Client, error) {
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

// GetKubernetesClientSetWithKubeconfig creates a typed Kubernetes clientset
// using the specified kubeconfig file path.
func GetKubernetesClientSetWithKubeconfig(kubeconfigPath string) (kubernetes.Interface, error) {
	config, err := GetConfigWithKubeconfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes clientset: %w", err)
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
