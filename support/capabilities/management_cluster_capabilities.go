package capabilities

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sync"

	routev1 "github.com/openshift/api/route/v1"
	batchv1 "k8s.io/api/batch/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

type CapabilityType int

const (
	// CapabilityRoute indicates if the management cluster supports routes
	CapabilityRoute CapabilityType = iota
	CapabilityV1Cronjob
)

// ManagementClusterCapabilities holds all information about optional capabilities of
// the management cluster.
type ManagementClusterCapabilities struct {
	capabilities map[CapabilityType]struct{}
	lock         sync.RWMutex
}

func (m *ManagementClusterCapabilities) Has(capabilities ...CapabilityType) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	// clearly define the behaviour if no capabilities are passed in
	if len(capabilities) == 0 {
		return false
	}
	for _, cap := range capabilities {
		if _, exists := m.capabilities[cap]; !exists {
			return false
		}
	}
	return true
}

// isGroupVersionRegistered determines if a specified groupVersion is registered on the cluster
func isGroupVersionRegistered(client discovery.ServerResourcesInterface, groupVersion schema.GroupVersion) (bool, error) {
	_, apis, err := client.ServerGroupsAndResources()
	if err != nil {
		if discovery.IsGroupDiscoveryFailedError(err) {
			// If the group we are looking for can't be fully discovered,
			// that does still mean that it exists.
			// Continue with the search in the discovered groups if not present here.
			e := err.(*discovery.ErrGroupDiscoveryFailed)
			if _, exists := e.Groups[groupVersion]; exists {
				return true, nil
			}
		} else {
			return false, err
		}
	}

	for _, api := range apis {
		if api.GroupVersion == groupVersion.String() {
			return true, nil
		}
	}

	return false, nil
}

// isObjectKindRegistered determines if a specified kube api-resource exists in the cluster
func isObjectKindRegistered(client discovery.ServerResourcesInterface, object schema.ObjectKind) (bool, error) {
	_, apis, err := client.ServerGroupsAndResources()
	for _, api := range apis {
		if api.GroupVersion == object.GroupVersionKind().GroupVersion().String() {
			for _, apiResource := range api.APIResources {
				if apiResource.Kind == object.GroupVersionKind().Kind {
					return true, nil
				}
			}
		}
	}
	if err != nil && discovery.IsGroupDiscoveryFailedError(err) {
		e := err.(*discovery.ErrGroupDiscoveryFailed)
		if _, exists := e.Groups[object.GroupVersionKind().GroupVersion()]; !exists {
			//the group was fully discovered and we confirmed the resource does not exist
			//can safely ignore error
			return false, nil
		}
	}
	return false, err
}

func DetectManagementClusterCapabilities(client discovery.ServerResourcesInterface) (*ManagementClusterCapabilities, error) {
	discoveredCapabilities := map[CapabilityType]struct{}{}
	hasRoutesCap, err := isGroupVersionRegistered(client, routev1.GroupVersion)
	if err != nil {
		return nil, err
	}
	if hasRoutesCap {
		discoveredCapabilities[CapabilityRoute] = struct{}{}
	}
	hasV1CronjobCap, err := isObjectKindRegistered(client, &batchv1.CronJob{
		TypeMeta: v1.TypeMeta{
			Kind:       "CronJob",
			APIVersion: batchv1.SchemeGroupVersion.String(),
		},
	})
	if err != nil {
		return nil, err
	}
	if hasV1CronjobCap {
		discoveredCapabilities[CapabilityV1Cronjob] = struct{}{}
	}
	return &ManagementClusterCapabilities{capabilities: discoveredCapabilities}, nil
}
