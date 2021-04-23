package kubeconfig

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubejson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	clientcmdapiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"
)

// TODO: NEXT: incorporate into an fzf workflow

const description string = `
This command renders a kubeconfig with a context for every HostedCluster resource.
The contexts are named based on the HostedCluster following the pattern:

    {hostedcluster.namespace}-{hostedcluster.name}

The kubeconfig for each cluster is based on the secret referenced by the status
of the HostedCluster itself.
`

// NewCreateCommand returns a command which creates a combined kubeconfig
// from HostedCluster resources.
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubeconfig",
		Short:        "Creates a combined kubeconfig from hostedcluster resources",
		Long:         description,
		SilenceUsage: true,
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		return render(ctx)
	}

	return cmd
}

// render builds the combined kubeconfig for hostedclusters and prints the
// kubeconfig to stdout.
func render(ctx context.Context) error {
	scheme := runtime.NewScheme()
	if err := clientcmdapiv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to set up scheme: %w", err)
	}
	serializer := kubejson.NewSerializerWithOptions(
		kubejson.DefaultMetaFactory, scheme, scheme,
		kubejson.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	c := util.GetClientOrDie()
	kubeConfig, err := buildCombinedConfig(ctx, c)
	if err != nil {
		return fmt.Errorf("failed to make kubeconfig: %w", err)
	}

	return serializer.Encode(kubeConfig, os.Stdout)
}

// NamedConfig adds a name to a Config.
type NamedConfig struct {
	*clientcmdapiv1.Config
	Name string
}

// buildCombinedConfig finds the kubeconfigs for all HostedClusters which report
// one and merges them into a single kubeconfig. The generated admin context for
// each cluster will follow the pattern: {hostedcluster.namespace}-{hostedcluster.name}
func buildCombinedConfig(ctx context.Context, c client.Client) (*clientcmdapiv1.Config, error) {
	// Select clusters.
	var clusters hyperv1.HostedClusterList
	if err := c.List(context.TODO(), &clusters); err != nil {
		return nil, fmt.Errorf("failed to list hostedclusters: %w", err)
	}
	var filtered []hyperv1.HostedCluster
	for i, cluster := range clusters.Items {
		if cluster.Status.KubeConfig == nil {
			log.Printf("skipping hostedcluster %s which reports no kubeconfig", client.ObjectKeyFromObject(&cluster))
			continue
		}
		filtered = append(filtered, clusters.Items[i])
	}
	log.Printf("selected %d of %d hostedclusters for the kubeconfig", len(filtered), len(clusters.Items))

	// Collect the cluster kubeconfigs and give them unique names.
	var clusterConfigs []NamedConfig
	for _, cluster := range filtered {
		log.Printf("adding %s/%s to kubeconfig", cluster.Namespace, cluster.Name)
		kubeConfigSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Namespace,
				Name:      cluster.Status.KubeConfig.Name,
			},
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(&kubeConfigSecret), &kubeConfigSecret); err != nil {
			log.Printf("failed to get kubeconfig secret %s: %s", client.ObjectKeyFromObject(&kubeConfigSecret), err)
			continue
		}
		data, hasData := kubeConfigSecret.Data["kubeconfig"]
		if !hasData || len(data) == 0 {
			log.Printf("missing kubeconfig contents")
			continue
		}
		var kubeConfig clientcmdapiv1.Config
		if err := yaml.Unmarshal(data, &kubeConfig); err != nil {
			log.Printf("failed to load kubeconfig: %s\nraw data:\n%s\n", err, data)
			continue
		}
		config := NamedConfig{
			Name:   cluster.Namespace + "-" + cluster.Name,
			Config: &kubeConfig,
		}
		clusterConfigs = append(clusterConfigs, config)
		log.Printf("added %s to kubeconfig", config.Name)
	}

	// Combine the cluster configs into a unified kubeconfig.
	merged := mergeClusterKubeConfigs(clusterConfigs)

	// Set a default context for convenience
	if len(merged.Contexts) > 0 {
		merged.CurrentContext = merged.Contexts[0].Name
	}

	log.Printf("created kubeconfig with %d contexts", len(merged.Contexts))

	return merged, nil
}

// mergeClusterKubeConfigs merges the given kubeconfigs by naming the cluster,
// auth, and context fields according to the given NamedConfig name which is
// assumed to be a unique name representing the HostedCluster.
//
// This function assumes the the first element of the cluster and auth fields
// combined represent an admin context for the cluster.
func mergeClusterKubeConfigs(clusterConfigs []NamedConfig) *clientcmdapiv1.Config {
	merged := clientcmdapiv1.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters:   []clientcmdapiv1.NamedCluster{},
		AuthInfos:  []clientcmdapiv1.NamedAuthInfo{},
		Contexts:   []clientcmdapiv1.NamedContext{},
	}

	for _, config := range clusterConfigs {
		configCluster := config.Clusters[0].Cluster
		configAuthInfo := config.AuthInfos[0].AuthInfo

		cluster := clientcmdapiv1.NamedCluster{
			Name:    config.Name,
			Cluster: configCluster,
		}
		authInfo := clientcmdapiv1.NamedAuthInfo{
			Name:     config.Name + "-admin",
			AuthInfo: configAuthInfo,
		}
		ctx := clientcmdapiv1.NamedContext{
			Name: config.Name,
			Context: clientcmdapiv1.Context{
				Cluster:   config.Name,
				AuthInfo:  authInfo.Name,
				Namespace: "default",
			},
		}
		merged.Clusters = append(merged.Clusters, cluster)
		merged.AuthInfos = append(merged.AuthInfos, authInfo)
		merged.Contexts = append(merged.Contexts, ctx)
	}

	return &merged
}
