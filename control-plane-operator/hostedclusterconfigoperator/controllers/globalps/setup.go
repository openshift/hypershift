package globalps

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	crreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
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

	// Create a cache for nodes (cluster-scoped)
	nodeCache, err := cache.New(opts.Manager.GetConfig(), cache.Options{
		Scheme: opts.Manager.GetScheme(),
		DefaultNamespaces: map[string]cache.Config{
			"": {}, // Empty string for cluster-scoped resources like nodes
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create node cache: %w", err)
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

	// Get the informer for nodes from node cache
	nodeInformer, err := nodeCache.GetInformer(ctx, &corev1.Node{})
	if err != nil {
		return fmt.Errorf("failed to get node informer: %w", err)
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

	// Add the caches to the manager
	if err := opts.Manager.Add(kubeSystemCache); err != nil {
		return fmt.Errorf("failed to add kube-system cache: %w", err)
	}
	if err := opts.Manager.Add(nodeCache); err != nil {
		return fmt.Errorf("failed to add node cache: %w", err)
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

	// Watch for nodes - when nodes are created, we need to reconcile global pull secret
	if err := c.Watch(&source.Informer{
		Informer: nodeInformer,
		Handler: handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o crclient.Object) []crreconcile.Request {
			// Trigger reconciliation for node creation using the node's name for better observability
			// The reconciler ignores the NamespacedName but this helps with logging and debugging
			return []crreconcile.Request{{NamespacedName: types.NamespacedName{Name: o.GetName(), Namespace: ""}}}
		}),
		Predicates: []predicate.Predicate{
			predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					// Only reconcile when new nodes are created
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Ignore node updates
					return false
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					// Ignore node deletions
					return false
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to watch nodes: %w", err)
	}

	return nil
}
