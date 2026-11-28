package machine

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

const (
	tenantServiceNameLabelKey         = "cluster.x-k8s.io/tenant-service-name"
	clusterNameLabelKey               = "cluster.x-k8s.io/cluster-name"
	kubevirtInfraAPIValidationTimeout = 30 * time.Second
	kubevirtInfraCacheSyncTimeout     = 2 * time.Minute
)

type reconciler struct {
	client              client.Client
	kubevirtInfraClient client.Client
	hcpKey              client.ObjectKey
	upsert.CreateOrUpdateProvider
}

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	// This controller is just needed at kubevirt to manage passthrough
	// endpointslices
	if opts.PlatformType != hyperv1.KubevirtPlatform {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	log.Info("Setup")
	kubevirtScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(kubevirtScheme)
	_ = kubevirtv1.AddToScheme(kubevirtScheme)
	_ = discoveryv1.AddToScheme(kubevirtScheme)

	kubevirtInfraDiscoveryConfig := rest.CopyConfig(opts.KubevirtInfraConfig)
	if kubevirtInfraDiscoveryConfig.Timeout == 0 || kubevirtInfraDiscoveryConfig.Timeout > kubevirtInfraAPIValidationTimeout {
		kubevirtInfraDiscoveryConfig.Timeout = kubevirtInfraAPIValidationTimeout
	}
	kubevirtHttpClient, err := rest.HTTPClientFor(kubevirtInfraDiscoveryConfig)
	if err != nil {
		return fmt.Errorf("failed creating kubevirt cluster http client: %w", err)
	}

	kubevirtMapper, err := apiutil.NewDynamicRESTMapper(kubevirtInfraDiscoveryConfig, kubevirtHttpClient)
	if err != nil {
		return fmt.Errorf("failed creating kubevirt cluster rest mapper: %w", err)
	}

	hcpKey := client.ObjectKey{
		Namespace: opts.Namespace,
		Name:      opts.HCPName,
	}
	hcp := &hyperv1.HostedControlPlane{}
	// Use uncached client so we don't have to start the cache before read
	// hcp, this is done only at hosted cluster startup so we are not going
	// to hit api server too much.
	if err := opts.CPCluster.GetAPIReader().Get(ctx, hcpKey, hcp); err != nil {
		return fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	infraNamespace := kubevirtInfraNamespace(hcp)
	// if kubevirt infra config is not used, it is being set the same as the mgmt config
	kubevirtInfraValidationConfig := rest.CopyConfig(kubevirtInfraDiscoveryConfig)
	kubevirtInfraValidationConfig.WarningHandler = rest.NoWarnings{}
	kubevirtInfraAPIClient, err := client.NewWithWatch(kubevirtInfraValidationConfig, client.Options{
		Scheme: kubevirtScheme,
		Mapper: kubevirtMapper,
	})
	if err != nil {
		return fmt.Errorf("failed to create kubevirt infra API client: %w", err)
	}
	validationCtx, cancelValidation := context.WithTimeout(ctx, kubevirtInfraAPIValidationTimeout)
	defer cancelValidation()
	if err := validateKubevirtInfraAccess(validationCtx, kubevirtInfraAPIClient, infraNamespace); err != nil {
		return err
	}

	kubevirtInfraCache, err := cache.New(opts.KubevirtInfraConfig, cache.Options{
		Scheme: kubevirtScheme,
		Mapper: kubevirtMapper,
		DefaultNamespaces: map[string]cache.Config{
			infraNamespace: cache.Config{}},
	})
	if err != nil {
		return fmt.Errorf("failed building kubevirt infra cache: %w", err)
	}
	kubevirtInfraClientRestConfig := rest.CopyConfig(opts.KubevirtInfraConfig)
	kubevirtInfraClientRestConfig.WarningHandler = rest.NoWarnings{}
	kubevirtInfraClient, err := client.New(kubevirtInfraClientRestConfig, client.Options{
		Scheme: kubevirtScheme,
		Mapper: kubevirtMapper,
		Cache:  &client.CacheOptions{Reader: kubevirtInfraCache},
	})
	if err != nil {
		return fmt.Errorf("failed to create kubevirt infra uncached client: %w", err)
	}
	r := &reconciler{
		client:                 opts.CPCluster.GetClient(),
		kubevirtInfraClient:    kubevirtInfraClient,
		hcpKey:                 hcpKey,
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}
	c, err := controller.New("machine", opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &capiv1.Machine{}, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch Machines: %w", err)
	}
	if _, err := kubevirtInfraCache.GetInformer(ctx, &corev1.Service{}, cache.BlockUntilSynced(false)); err != nil {
		return fmt.Errorf("failed to initialize kubevirt service informer: %w", err)
	}
	if err := opts.Manager.Add(kubevirtInfraCache); err != nil {
		return fmt.Errorf("failed to add kubevirt infra cache: %w", err)
	}
	if err := opts.Manager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return waitForKubevirtInfraCacheSync(ctx, kubevirtInfraCache, kubevirtInfraCacheSyncTimeout)
	})); err != nil {
		return fmt.Errorf("failed to add kubevirt infra cache sync check: %w", err)
	}
	allNodes := func(watchContext context.Context, obj client.Object) []reconcile.Request {
		machineList := &capiv1.MachineList{}
		if err := r.client.List(watchContext, machineList); err != nil {
			log.Error(err, "failed listing machines at kubevirt service watch function")
			return nil
		}
		requests := []reconcile.Request{}
		for _, machine := range machineList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: machine.Namespace,
					Name:      machine.Name,
				},
			})
		}
		return requests
	}
	isKCCMService := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		serviceLabels := obj.GetLabels()
		if len(serviceLabels) == 0 {
			return false
		}
		_, ok := serviceLabels[tenantServiceNameLabelKey]
		if !ok {
			return false
		}
		obtainedClusterName, ok := serviceLabels[clusterNameLabelKey]
		if !ok || obtainedClusterName != hcp.Labels[clusterNameLabelKey] {
			return false
		}
		return true
	})
	if err := c.Watch(source.Kind[client.Object](kubevirtInfraCache, &corev1.Service{}, handler.EnqueueRequestsFromMapFunc(allNodes), isKCCMService)); err != nil {
		return fmt.Errorf("failed to watch kubevirt services: %w", err)
	}

	return nil
}

func validateKubevirtInfraAccess(ctx context.Context, kubevirtInfraClient client.WithWatch, namespace string) error {
	if err := kubevirtInfraClient.List(ctx, &corev1.ServiceList{}, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list services in kubevirt infra namespace %q: %w", namespace, err)
	}
	watcher, err := kubevirtInfraClient.Watch(ctx, &corev1.ServiceList{}, client.InNamespace(namespace))
	if err != nil {
		return fmt.Errorf("failed to watch services in kubevirt infra namespace %q: %w", namespace, err)
	}
	watcher.Stop()
	return nil
}

func waitForKubevirtInfraCacheSync(ctx context.Context, kubevirtInfraCache cache.Cache, timeout time.Duration) error {
	syncCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if kubevirtInfraCache.WaitForCacheSync(syncCtx) {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("timed out after %s waiting for kubevirt infra cache to sync", timeout)
}
