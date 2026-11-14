package hostedcontrolplane

import (
	"context"
	"errors"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type azureLocationClient struct {
	vnet          func(context.Context, string, string, azcore.TokenCredential, string) (armnetwork.VirtualNetworksClientGetResponse, error)
	securityGroup func(context.Context, string, string, azcore.TokenCredential, string) (armnetwork.SecurityGroupsClientGetResponse, error)
	resourceGroup func(context.Context, string, string, azcore.TokenCredential, string) (armresources.ResourceGroupsClientGetResponse, error)
}

type azureLocationRetryError struct {
	cause    error
	deadline time.Time
}

func (retry *azureLocationRetryError) Error() string {
	return retry.cause.Error()
}

func (retry *azureLocationRetryError) Unwrap() error {
	return retry.cause
}

type azureLocationValidationEntry struct {
	sync.Mutex
	uid        types.UID
	generation int64
	retry      *azureLocationRetryError
}

type azureLocationValidationCache struct {
	sync.Mutex
	entries map[types.NamespacedName]*azureLocationValidationEntry
}

func (cache *azureLocationValidationCache) entry(hcp *hyperv1.HostedControlPlane) *azureLocationValidationEntry {
	cache.Lock()
	defer cache.Unlock()
	if cache.entries == nil {
		cache.entries = map[types.NamespacedName]*azureLocationValidationEntry{}
	}
	key := client.ObjectKeyFromObject(hcp)
	entry := cache.entries[key]
	if entry == nil || entry.uid != hcp.UID || entry.generation != hcp.Generation {
		entry = &azureLocationValidationEntry{uid: hcp.UID, generation: hcp.Generation}
		cache.entries[key] = entry
	}
	return entry
}

func (cache *azureLocationValidationCache) remove(key types.NamespacedName, entry *azureLocationValidationEntry) {
	cache.Lock()
	defer cache.Unlock()
	if entry == nil || cache.entries[key] == entry {
		delete(cache.entries, key)
	}
}

func (cache *azureLocationValidationCache) invalidate(hcp *hyperv1.HostedControlPlane) {
	cache.Lock()
	defer cache.Unlock()
	key := client.ObjectKeyFromObject(hcp)
	if entry := cache.entries[key]; entry != nil && (entry.uid != hcp.UID || entry.generation != hcp.Generation) {
		delete(cache.entries, key)
	}
}

func (r *HostedControlPlaneReconciler) azureLocationClock() clock.Clock {
	if r.clock != nil {
		return r.clock
	}
	return clock.RealClock{}
}

func (r *HostedControlPlaneReconciler) azureLocationRetryDelay(err error) time.Duration {
	var retry *azureLocationRetryError
	if !errors.As(err, &retry) {
		return 0
	}
	return max(retry.deadline.Sub(r.azureLocationClock().Now()), time.Nanosecond)
}
