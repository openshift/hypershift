package controlplanecomponent

import (
	"context"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type gvkAccessibility int

const (
	gvkAccessible gvkAccessibility = iota

	gvkInaccessible
)

// GVKAccessChecker determines if a resource type is accessible (i.e. the controller
// has RBAC and the CRD exists). Implementations may cache probe results.
type GVKAccessChecker interface {
	// GetOrProbe checks the cache for the given object's GVK. On a cache miss it
	// performs a single uncached Get to probe accessibility. The result is:
	//   - 403 Forbidden  → inaccessible (cached, returns false, nil)
	//   - NoMatch        → inaccessible (cached, returns false, nil)
	//   - 404 NotFound   → accessible   (cached, returns true, nil)
	//   - success        → accessible   (cached, returns true, nil)
	//   - other error    → not cached   (returns false, err)
	GetOrProbe(ctx context.Context, obj client.Object) (accessible bool, err error)
}

// gvkAccessCache caches whether a GVK is accessible. This avoids creating
// informers via the cached client for resource types the CPO cannot access,
// which would otherwise retry LIST/WATCH forever and block reconciliation.
type gvkAccessCache struct {
	uncachedReader client.Reader
	cache          sync.Map // schema.GroupVersionKind -> gvkAccessibility
}

// NewGVKAccessCache returns a GVKAccessChecker that probes GVK accessibility
// using the given uncached reader and caches the results.
func NewGVKAccessCache(uncachedReader client.Reader) GVKAccessChecker {
	return &gvkAccessCache{
		uncachedReader: uncachedReader,
	}
}

func (c *gvkAccessCache) GetOrProbe(ctx context.Context, obj client.Object) (bool, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		// If GVK is not set, we can't probe – fall through to the cached client.
		return true, nil
	}

	if val, ok := c.cache.Load(gvk); ok {
		return val.(gvkAccessibility) == gvkAccessible, nil
	}

	probe := obj.DeepCopyObject().(client.Object)
	err := c.uncachedReader.Get(ctx, client.ObjectKeyFromObject(obj), probe)
	accessible, probeErr := c.handleProbeResult(gvk, err)
	if probeErr == nil && !accessible {
		log := ctrl.LoggerFrom(ctx)
		log.Info("Skipping inaccessible resource type: no RBAC permission or CRD not installed",
			"gvk", gvk, "reason", err.Error())
	}
	return accessible, probeErr
}

func (c *gvkAccessCache) handleProbeResult(gvk schema.GroupVersionKind, err error) (bool, error) {
	if err == nil {
		c.cache.Store(gvk, gvkAccessible)
		return true, nil
	}

	if apierrors.IsNotFound(err) {
		c.cache.Store(gvk, gvkAccessible)
		return true, nil
	}

	if apierrors.IsForbidden(err) || meta.IsNoMatchError(err) {
		c.cache.Store(gvk, gvkInaccessible)
		return false, nil
	}

	// Transient error – don't cache, let caller retry.
	return false, err
}
