package globalps

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	// Create a predicate for the pull-secret
	secretPredicate := predicate.NewPredicateFuncs(func(o crclient.Object) bool {
		return o.GetNamespace() == "kube-system"
	})

	// Create a cache for the kube-system namespace
	kubeSystemCache, err := cache.New(opts.Manager.GetConfig(), cache.Options{
		Scheme: opts.Manager.GetScheme(),
		DefaultNamespaces: map[string]cache.Config{
			"kube-system": {},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create kube-system cache: %w", err)
	}

	// Create a crclient from new cache
	kubeSystemClient, err := crclient.New(opts.Manager.GetConfig(), crclient.Options{
		Scheme: opts.Manager.GetScheme(),
		Cache:  &crclient.CacheOptions{Reader: kubeSystemCache},
	})
	if err != nil {
		return fmt.Errorf("failed to create kube-system client: %w", err)
	}

	// Get the informer for secrets from new cache
	kubeSystemSecretInformer, err := kubeSystemCache.GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return fmt.Errorf("failed to get kube-system secret informer: %w", err)
	}

	uncachedClientRestConfig := opts.Manager.GetConfig()
	uncachedClientRestConfig.WarningHandler = rest.NoWarnings{}
	uncachedClient, err := crclient.New(uncachedClientRestConfig, crclient.Options{
		Scheme: opts.Manager.GetScheme(),
		Mapper: opts.Manager.GetRESTMapper(),
	})
	if err != nil {
		return fmt.Errorf("failed to create uncached client: %w", err)
	}

	hccoImage := os.Getenv("HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE")
	if hccoImage == "" {
		return fmt.Errorf("HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE is not set")
	}

	r := &Reconciler{
		cpClient:               opts.CPCluster.GetClient(),
		hcUncachedClient:       uncachedClient,
		kubeSystemSecretClient: kubeSystemClient,
		hcpNamespace:           opts.Namespace,
		hccoImage:              hccoImage,
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}

	// Add the cache to the manager
	if err := opts.Manager.Add(kubeSystemCache); err != nil {
		return fmt.Errorf("failed to add kube-system cache: %w", err)
	}

	// Create a controller
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	// Watch for secrets in kube-system
	if err := c.Watch(&source.Informer{
		Informer: kubeSystemSecretInformer,
		Handler:  &handler.EnqueueRequestForObject{},
		Predicates: []predicate.Predicate{
			secretPredicate,
		},
	}); err != nil {
		return fmt.Errorf("failed to watch kube-system secrets: %w", err)
	}

	return nil
}
