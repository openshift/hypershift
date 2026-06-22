package hostedcluster

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultSecurityContextUIDAnnnotation is used to store the allocated UID in namespace annotations
	DefaultSecurityContextUIDAnnnotation = "hypershift.openshift.io/default-security-context-uid"
	// minSecurityContextUID is the starting point for UID allocation
	minSecurityContextUID = controlplanecomponent.DefaultSecurityContextUID
	// maxSecurityContextUIDs is the maximum number of UIDs we'll allocate
	maxSecurityContextUIDs = 10000
	// refreshInterval is how often we refresh the allocator state from namespaces
	refreshInterval = 8 * time.Hour
)

// securityContextUIDAllocator manages allocation of UIDs for control plane namespaces
type securityContextUIDAllocator struct {
	sync.Mutex
	// allocatedUIDs tracks which UIDs are in use
	allocatedUIDs map[int64]struct{}
	// initialized indicates if the allocator has been initialized from existing namespaces
	initialized bool
	// lastRefresh tracks when we last refreshed from namespaces
	lastRefresh time.Time
}

var globalUIDAllocator = &securityContextUIDAllocator{
	allocatedUIDs: make(map[int64]struct{}),
}

// initializeFromNamespaces scans existing namespaces to initialize the allocator
func (a *securityContextUIDAllocator) initializeFromNamespaces(ctx context.Context, c client.Client) error {
	labelSelector := labels.SelectorFromSet(labels.Set{
		ControlPlaneNamespaceLabelKey: "true",
	})

	namespaceList := &corev1.NamespaceList{}
	if err := c.List(ctx, namespaceList, &client.ListOptions{
		LabelSelector: labelSelector,
	}); err != nil {
		return err
	}

	// Clear existing allocations before refreshing
	a.allocatedUIDs = make(map[int64]struct{})

	for _, ns := range namespaceList.Items {
		uidStr, ok := ns.Annotations[DefaultSecurityContextUIDAnnnotation]
		if !ok {
			continue
		}
		uid, err := strconv.ParseInt(uidStr, 10, 64)
		if err != nil {
			continue
		}
		if uid >= minSecurityContextUID && uid < minSecurityContextUID+maxSecurityContextUIDs {
			a.allocatedUIDs[uid] = struct{}{}
		}
	}
	a.initialized = true
	a.lastRefresh = time.Now()
	return nil
}

// allocateUID allocates a new UID or returns an error if none available
func (a *securityContextUIDAllocator) allocateUID(ctx context.Context, c client.Client) (int64, error) {
	a.Lock()
	defer a.Unlock()

	// Initialize if not initialized or refresh if it's time
	if !a.initialized || time.Since(a.lastRefresh) > refreshInterval {
		if err := a.initializeFromNamespaces(ctx, c); err != nil {
			return 0, err
		}
	}

	// Always scan from the beginning to prefer lower UIDs
	for uid := minSecurityContextUID; uid < minSecurityContextUID+maxSecurityContextUIDs; uid++ {
		if _, inUse := a.allocatedUIDs[uid]; !inUse {
			a.allocatedUIDs[uid] = struct{}{}
			return uid, nil
		}
	}

	return 0, fmt.Errorf("no free UIDs available in range %d to %d", minSecurityContextUID, minSecurityContextUID+maxSecurityContextUIDs-1)
}

// getNextAvailableSecurityContextUID returns the next available UID for a control plane namespace
func getNextAvailableSecurityContextUID(ctx context.Context, c client.Client) (int64, error) {
	return globalUIDAllocator.allocateUID(ctx, c)
}
