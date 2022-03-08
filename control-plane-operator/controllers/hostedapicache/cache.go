package hostedapicache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// HostedAPICache is used to provide read-only access to a hosted API server.
// The intent is for controllers (e.g. the hostedcontrolplane controller) to
// work with the cache instead of the apiserver directly when possible to avoid
// chatty queries or polling loops, and to provide a way for controllers to
// trigger reconciliation in response to events which happen in the hosted
// apiserver itself.
type HostedAPICache interface {
	// Reader provides the basic Get/List functions for resources and abstracts
	// access to the underlying cache.
	client.Reader

	// Events will receive a GenericEvent whenever a relevant resource change
	// happens within the hosted apiserver.
	Events() <-chan event.GenericEvent
}

// NotInitializedError means the cache isn't yet ready to use.
var ErrNotInitialized = errors.New("api cache hasn't yet been initialized")

// hostedAPICache is the HostedAPICache implementation backed by a cache.Cache
// and a set of event handlers wired to that cache. The cache is rebuilt in
// response to an update call given a kubeconfig, and will only rebuild if
// the kubeconfig bytes have changed since the last update.
//
// The cache will continue to run and process events until ctx is cancelled.
type hostedAPICache struct {
	ctx  context.Context
	log  logr.Logger
	lock sync.Mutex

	scheme *runtime.Scheme
	mapper meta.RESTMapper

	kubeConfig     []byte
	cache          cache.Cache
	events         chan event.GenericEvent
	cancelCacheCtx context.CancelFunc
}

// newHostedAPICache returns a new hostedAPICache. The context passed here is
// used to drive the cache itself and should be used for graceful termination
// of the process for an overall shutdown.
func newHostedAPICache(ctx context.Context, log logr.Logger, scheme *runtime.Scheme) *hostedAPICache {
	return &hostedAPICache{
		ctx:    ctx,
		log:    log,
		scheme: scheme,
		cache:  nil,
		events: make(chan event.GenericEvent),
	}
}

// destroy cancels and clears the current cache and forgets the kubeconfig.
func (h *hostedAPICache) destroy() {
	h.lock.Lock()
	defer h.lock.Unlock()

	// Shut down any existing cache
	if h.cancelCacheCtx != nil {
		h.cancelCacheCtx()
	}
	h.cache = nil
	h.cancelCacheCtx = nil
	h.kubeConfig = nil
}

// update checks the newKubeConfig, and if that differs from the current kubeconfig,
// rebuilds the cache using the new kubeconfig. The triggerObj parameter is the
// object which will be associated with any GenericEvents sent to the event
// channel. For now this will always be a HostedControlPlane resource.
//
// Note that when the cache is rebuilt, the cache is started asynchronously. This
// function does not block awaiting cache sync.
//
// The requestCtx is used for any blocking calls for the scope of the cache rebuild
// operation; the cache itself will continue to process events until the ctx
// associated with the hostedAPICache is cancelled.
//
// For now, the event handlers installed in the cache are statically defined within
// this function and are limited to a set of significant changes to ClusterVersion
// resources (add, delete, status updated).
func (h *hostedAPICache) update(requestCtx context.Context, triggerObj client.Object, newKubeConfig []byte) error {
	h.lock.Lock()
	defer h.lock.Unlock()

	if len(newKubeConfig) == 0 {
		return fmt.Errorf("kube config is empty")
	}

	// Only rebuild the cache if the kubeconfig has changed
	if bytes.Equal(newKubeConfig, h.kubeConfig) {
		return nil
	}

	h.log.Info("rebuilding api cache")

	// Initialize a new cache
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(newKubeConfig)
	if err != nil {
		return fmt.Errorf("invalid kube config: %w", err)
	}
	guestCluster, err := cluster.New(restConfig, func(opt *cluster.Options) {
		opt.Scheme = h.scheme
		opt.MapperProvider = func(c *rest.Config) (meta.RESTMapper, error) {
			var err error
			if h.mapper == nil {
				h.mapper, err = apiutil.NewDynamicRESTMapper(c)
				if err != nil {
					return nil, err
				}
			}
			return h.mapper, nil
		}
	})
	if err != nil {
		return fmt.Errorf("failed to create controller-runtime cluster for guest: %w", err)
	}
	newCache := guestCluster.GetCache()
	// Initialize event handlers for the new cache
	err = func(c cache.Cache) error {
		informer, err := c.GetInformerForKind(requestCtx, schema.GroupVersionKind{
			Group:   configv1.GroupName,
			Version: configv1.GroupVersion.Version,
			Kind:    "ClusterVersion",
		})
		if err != nil {
			return fmt.Errorf("failed to set up clusterversion informer: %w", err)
		}
		informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				clusterVersion := obj.(*configv1.ClusterVersion)
				h.log.Info("triggering event for clusterversion add", "name", clusterVersion.Name)
				h.events <- event.GenericEvent{Object: triggerObj}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldClusterVersion := oldObj.(*configv1.ClusterVersion)
				newClusterVersion := newObj.(*configv1.ClusterVersion)
				if !equality.Semantic.DeepEqual(oldClusterVersion.Status, newClusterVersion.Status) {
					h.log.Info("triggering event for clusterversion update", "name", oldClusterVersion.Name)
					h.events <- event.GenericEvent{Object: triggerObj}
				}
			},
			DeleteFunc: func(obj interface{}) {
				clusterVersion := obj.(*configv1.ClusterVersion)
				h.log.Info("triggering event for clusterversion delete", "operator", clusterVersion.Name)
				h.events <- event.GenericEvent{Object: triggerObj}
			},
		})
		return nil
	}(newCache)
	if err != nil {
		return fmt.Errorf("failed to initialize cache event handlers: %w", err)
	}

	// Shut down any existing cache
	if h.cancelCacheCtx != nil {
		h.cancelCacheCtx()
	}

	// Replace the existing cache
	newCacheCtx, newCancelCache := context.WithCancel(h.ctx)
	h.cache = newCache
	h.cancelCacheCtx = newCancelCache
	h.kubeConfig = newKubeConfig

	// Start the new cache
	go func() {
		if err := h.cache.Start(newCacheCtx); err != nil {
			h.log.Error(err, "failed to start hosted api cache")
		} else {
			h.log.Info("hosted api cache gracefully stopped")
		}
	}()

	h.log.Info("rebuilt api cache")
	return nil
}

func (h *hostedAPICache) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if h.cache == nil {
		return ErrNotInitialized
	}
	return h.cache.Get(ctx, key, obj)
}

func (h *hostedAPICache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if h.cache == nil {
		return ErrNotInitialized
	}
	return h.cache.List(ctx, list, opts...)
}

func (h *hostedAPICache) Events() <-chan event.GenericEvent {
	return h.events
}
