package provisioning

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// newClusterClient builds a client for the cluster identified by kubeConfigFile, falling back
// to $KUBECONFIG and then ~/.kube/config when it is empty.
func newClusterClient(kubeConfigFile string) (client.Client, error) {
	restConfig, err := loadRESTConfig(kubeConfigFile)
	if err != nil {
		return nil, err
	}

	kubeClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return kubeClient, nil
}

func loadRESTConfig(kubeConfigFile string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeConfigFile != "" {
		loadingRules.ExplicitPath = kubeConfigFile
	}

	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig, pass --kubeconfig or set $KUBECONFIG: %w", err)
	}

	return restConfig, nil
}
