package capabilities

import (
	"sync"

	configv1 "github.com/openshift/api/config/v1"
	imagestreamv1 "github.com/openshift/api/image/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

type CapabiltyChecker interface {
	Has(capabilities ...CapabilityType) bool
}

type MockCapabilityChecker struct {
	MockHas func(capabilities ...CapabilityType) bool
}

func (m *MockCapabilityChecker) Has(capabilities ...CapabilityType) bool {
	return m.MockHas(capabilities...)
}

type CapabilityType int

const (
	// CapabilityRoute indicates if the management cluster supports routes
	CapabilityRoute CapabilityType = iota

	// CapabilitySecurityContextConstraint indicates if the management cluster
	// supports security context constraints
	CapabilitySecurityContextConstraint

	// CapabilityImage indicates if the cluster supports the
	// image.config.openshift.io api
	CapabilityImage

	// CapabilityInfrastructure indicates if the cluster supports the
	// infrastructures.config.openshift.io api
	CapabilityInfrastructure

	// CapabilityIngress indicates if the cluster supports the
	// ingresses.config.openshift.io api
	CapabilityIngress

	// CapabilityProxy indicates if the cluster supports the
	// proxies.config.openshift.io api
	CapabilityProxy

	// CapabilityDNS indicates if the cluster supports the
	// dnses.config.openshift.io api
	CapabilityDNS

	// CapabilityNetworks indicates if the cluster supports the
	// networks.config.openshift.io api
	CapabilityNetworks

	// CapabilityICSP indicates if the cluster supports ImageContentSourcePolicy CRDs
	CapabilityICSP

	// CapabilityIDMS indicates if the cluster supports ImageDigestMirrorSet CRDs
	CapabilityIDMS

	// CapabilityImageStream indicates if the cluster supports ImageStream
	// image.openshift.io
	CapabilityImageStream
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
	// clearly define the behavior if no capabilities are passed in
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

// isAPIResourceRegistered determines if a specified API resource is registered on the cluster
func isAPIResourceRegistered(client discovery.ServerResourcesInterface, groupVersion schema.GroupVersion, resourceName string) (bool, error) {
	apis, err := client.ServerResourcesForGroupVersion(groupVersion.String())
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	if apis != nil {
		for _, api := range apis.APIResources {
			if api.Name == resourceName || api.SingularName == resourceName {
				return true, nil
			}
		}
	}

	return false, nil
}

func DetectManagementClusterCapabilities(client discovery.ServerResourcesInterface) (*ManagementClusterCapabilities, error) {
	discoveredCapabilities := map[CapabilityType]struct{}{}

	// check for route capability
	hasRouteCap, err := isAPIResourceRegistered(client, routev1.GroupVersion, "routes")
	if err != nil {
		return nil, err
	}
	if hasRouteCap {
		discoveredCapabilities[CapabilityRoute] = struct{}{}
	}

	// check for scc capability
	hasSccCap, err := isAPIResourceRegistered(client, securityv1.GroupVersion, "securitycontextconstraints")
	if err != nil {
		return nil, err
	}
	if hasSccCap {
		discoveredCapabilities[CapabilitySecurityContextConstraint] = struct{}{}
	}

	// check for image capability
	hasImageCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "image")
	if err != nil {
		return nil, err
	}
	if hasImageCap {
		discoveredCapabilities[CapabilityImage] = struct{}{}
	}

	// check for infrastructure capability
	hasInfraCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "infrastructures")
	if err != nil {
		return nil, err
	}
	if hasInfraCap {
		discoveredCapabilities[CapabilityInfrastructure] = struct{}{}
	}

	// check for ingress capability
	hasIngressCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "ingresses")
	if err != nil {
		return nil, err
	}
	if hasIngressCap {
		discoveredCapabilities[CapabilityIngress] = struct{}{}
	}

	// check for proxy capability
	hasProxyCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "proxies")
	if err != nil {
		return nil, err
	}
	if hasProxyCap {
		discoveredCapabilities[CapabilityProxy] = struct{}{}
	}

	// check for dns capability
	hasDNSCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "dnses")
	if err != nil {
		return nil, err
	}
	if hasDNSCap {
		discoveredCapabilities[CapabilityDNS] = struct{}{}
	}

	// check for networks capability
	hasNetworksCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "networks")
	if err != nil {
		return nil, err
	}
	if hasNetworksCap {
		discoveredCapabilities[CapabilityNetworks] = struct{}{}
	}

	// check for ImageContentSourcePolicy capability
	hasICSPCap, err := isAPIResourceRegistered(client, operatorv1alpha1.GroupVersion, "imagecontentsourcepolicies")
	if err != nil {
		return nil, err
	}
	if hasICSPCap {
		discoveredCapabilities[CapabilityICSP] = struct{}{}
	}

	// check for ImageDigestMirrorSet capability
	hasIDMSCap, err := isAPIResourceRegistered(client, configv1.GroupVersion, "imagedigestmirrorsets")
	if err != nil {
		return nil, err
	}
	if hasIDMSCap {
		discoveredCapabilities[CapabilityIDMS] = struct{}{}
	}

	// check for ImageStream capability
	hasImageStreamCap, err := isAPIResourceRegistered(client, imagestreamv1.GroupVersion, "imagestream")
	if err != nil {
		return nil, err
	}
	if hasImageStreamCap {
		discoveredCapabilities[CapabilityImageStream] = struct{}{}
	}

	return &ManagementClusterCapabilities{capabilities: discoveredCapabilities}, nil
}
