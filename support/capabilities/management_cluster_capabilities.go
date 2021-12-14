package capabilities

import (
	"sync"

	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

type CapabilityType int

const (
	// CapabilityRoute indicates if the management cluster supports routes
	CapabilityRoute CapabilityType = iota

	// CapabilitySecurityContextConstraint indicates if the management cluster
	// supports security context constraints
	CapabilitySecurityContextConstraint
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

func DetectManagementClusterCapabilities(client discovery.ServerResourcesInterface) (*ManagementClusterCapabilities, error) {
	discoveredCapabilities := map[CapabilityType]struct{}{}

	// check for route capability
	hasRoutesCap, err := isGroupVersionRegistered(client, routev1.GroupVersion)
	if err != nil {
		return nil, err
	}
	if hasRoutesCap {
		discoveredCapabilities[CapabilityRoute] = struct{}{}
	}

	// check for Scc capability
	hasSccCap, err := isGroupVersionRegistered(client, securityv1.GroupVersion)
	if err != nil {
		return nil, err
	}
	if hasSccCap {
		discoveredCapabilities[CapabilitySecurityContextConstraint] = struct{}{}
	}

	return &ManagementClusterCapabilities{capabilities: discoveredCapabilities}, nil
}
