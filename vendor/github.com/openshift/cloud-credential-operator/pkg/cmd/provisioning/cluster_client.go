package provisioning

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
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

	clusterScheme, err := newClusterScheme()
	if err != nil {
		return nil, err
	}

	kubeClient, err := client.New(restConfig, client.Options{Scheme: clusterScheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return kubeClient, nil
}

// newClusterScheme registers the Kubernetes and OpenShift API types ccoctl
// uses when reading cluster configuration and applying changes to it.
func newClusterScheme() (*runtime.Scheme, error) {
	clusterScheme := runtime.NewScheme()
	for _, addToScheme := range []func(*runtime.Scheme) error{
		scheme.AddToScheme,
		configv1.AddToScheme,
		operatorv1.AddToScheme,
	} {
		if err := addToScheme(clusterScheme); err != nil {
			return nil, fmt.Errorf("failed to build client scheme: %w", err)
		}
	}
	return clusterScheme, nil
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
