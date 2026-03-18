package capabilities

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
)

var _ CapabiltyChecker = &ManagementClusterCapabilities{}

var apiResourcesHyperShift = metav1.APIResourceList{
	GroupVersion: hyperv1.GroupVersion.String(),
	APIResources: []metav1.APIResource{
		{
			Name:         "hostedclusters",
			SingularName: "hostedcluster",
		},
	},
}

var apiResourcesRoute = metav1.APIResourceList{
	GroupVersion: routev1.GroupVersion.String(),
	APIResources: []metav1.APIResource{
		{
			Name:         "routes",
			SingularName: "route",
		},
	},
}

var apiResourcesScc = metav1.APIResourceList{
	GroupVersion: securityv1.GroupVersion.String(),
	APIResources: []metav1.APIResource{
		{
			Name:         "securitycontextconstraints",
			SingularName: "securitycontextconstraint",
		},
	},
}

var apiResourcesInfra = metav1.APIResourceList{
	GroupVersion: configv1.GroupVersion.String(),
	APIResources: []metav1.APIResource{
		{
			Name:         "infrastructures",
			SingularName: "infrastructure",
		},
	},
}

var apiResourcesConfigMulti = metav1.APIResourceList{
	GroupVersion: configv1.GroupVersion.String(),
	APIResources: []metav1.APIResource{
		{
			Name:         "infrastructures",
			SingularName: "infrastructure",
		},
		{
			Name:         "ingresses",
			SingularName: "ingress",
		},
		{
			Name:         "proxies",
			SingularName: "proxy",
		},
	},
}

func TestIsAPIResourceRegistered(t *testing.T) {

	testCases := []struct {
		name         string
		client       discovery.ServerResourcesInterface
		groupVersion schema.GroupVersion
		resourceName string
		resultErr    error
		isRegistered bool
		shouldError  bool
	}{
		{
			name:         "should return false if routes are not registered",
			client:       newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift),
			groupVersion: routev1.GroupVersion,
			resourceName: "routes",
			resultErr:    nil,
			isRegistered: false,
			shouldError:  false,
		},
		{
			name:         "should return true if routes are registered",
			client:       newFailableFakeDiscoveryClient(nil, apiResourcesRoute),
			groupVersion: routev1.GroupVersion,
			resourceName: "routes",
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name:         "should return true if singular names are used",
			client:       newFailableFakeDiscoveryClient(nil, apiResourcesRoute),
			groupVersion: routev1.GroupVersion,
			resourceName: "route",
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should fail on arbitrary errors",
			client: newFailableFakeDiscoveryClient(
				fmt.Errorf("ups"),
				metav1.APIResourceList{},
			),
			groupVersion: routev1.GroupVersion,
			resourceName: "",
			resultErr:    fmt.Errorf("ups"),
			isRegistered: false,
			shouldError:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isAPIResourceRegistered(tc.client, tc.groupVersion, tc.resourceName)
			g := NewGomegaWithT(t)
			g.Expect(got).To(Equal(tc.isRegistered))
			if tc.shouldError {
				g.Expect(err).To(Equal(tc.resultErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestDetectManagementCapabilities(t *testing.T) {

	testCases := []struct {
		name           string
		client         ManagementClusterDiscoveryClient
		capabilityType CapabilityType
		resultErr      error
		isRegistered   bool
		shouldError    bool
	}{
		{
			name:           "should return false if routes are not registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift),
			capabilityType: CapabilityRoute,
			resultErr:      nil,
			isRegistered:   false,
			shouldError:    false,
		},
		{
			name:           "should return true if routes are registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute),
			capabilityType: CapabilityRoute,
			resultErr:      nil,
			isRegistered:   true,
			shouldError:    false,
		},
		{
			name:           "should return false if scc is not registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute),
			capabilityType: CapabilitySecurityContextConstraint,
			resultErr:      nil,
			isRegistered:   false,
			shouldError:    false,
		},
		{
			name:           "should return true if scc is registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc),
			capabilityType: CapabilitySecurityContextConstraint,
			resultErr:      nil,
			isRegistered:   true,
			shouldError:    false,
		},
		{
			name:           "should return false if infrastructure is not registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc),
			capabilityType: CapabilityInfrastructure,
			resultErr:      nil,
			isRegistered:   false,
			shouldError:    false,
		},
		{
			name:           "should return true if infrastructure is registered",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc, apiResourcesInfra),
			capabilityType: CapabilityInfrastructure,
			resultErr:      nil,
			isRegistered:   true,
			shouldError:    false,
		},
		{
			name:           "should return false if partial resources are registered (same group version)",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc, apiResourcesInfra),
			capabilityType: CapabilityIngress,
			resultErr:      nil,
			isRegistered:   false,
			shouldError:    false,
		},
		{
			name:           "should return true if ingress is registered (same group version)",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc, apiResourcesConfigMulti),
			capabilityType: CapabilityIngress,
			resultErr:      nil,
			isRegistered:   true,
			shouldError:    false,
		},
		{
			name:           "should return true if proxy is registered (same group version)",
			client:         newFailableFakeDiscoveryClient(nil, apiResourcesHyperShift, apiResourcesRoute, apiResourcesScc, apiResourcesConfigMulti),
			capabilityType: CapabilityProxy,
			resultErr:      nil,
			isRegistered:   true,
			shouldError:    false,
		},
		{
			name: "should fail on arbitrary errors",
			client: newFailableFakeDiscoveryClient(
				fmt.Errorf("ups"),
				metav1.APIResourceList{},
			),
			resultErr:    fmt.Errorf("ups"),
			isRegistered: false,
			shouldError:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectManagementClusterCapabilities(tc.client)
			g := NewGomegaWithT(t)
			if tc.shouldError {
				g.Expect(err).To(Equal(tc.resultErr))
			} else {
				g.Expect(got.Has(tc.capabilityType)).To(Equal(tc.isRegistered))
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func newFailableFakeDiscoveryClient(err error, discovered ...metav1.APIResourceList) fakeFailableDiscoveryClient {
	discoveryClient := fakeFailableDiscoveryClient{
		Resources: []*metav1.APIResourceList{},
	}
	for _, apiResourceList := range discovered {
		discoveryClient.Resources = append(
			discoveryClient.Resources,
			&apiResourceList,
		)
	}
	discoveryClient.err = err
	return discoveryClient
}

// fakeFailableDiscoveryClient is a custom implementation of ManagementClusterDiscoveryClient.
// Existing fake clients are not flexible enough to express all resource and error responses relevant for testing.
type fakeFailableDiscoveryClient struct {
	Resources  []*metav1.APIResourceList
	err        error
	gitVersion string
}

func (f fakeFailableDiscoveryClient) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	for _, resource := range f.Resources {
		if resource.GroupVersion == groupVersion {
			return resource, nil
		}
	}
	return nil, f.err
}

func (f fakeFailableDiscoveryClient) ServerResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerVersion() (*apiversion.Info, error) {
	if f.gitVersion == "" {
		return &apiversion.Info{GitVersion: "v1.30.0"}, nil
	}
	return &apiversion.Info{GitVersion: f.gitVersion}, nil
}


func TestDetectNativeSidecarCapability(t *testing.T) {
	tests := []struct {
		name            string
		gitVersion      string
		expectedSupport bool
	}{
		{
			name:            "When K8s version is 1.29.0 it should support native sidecars",
			gitVersion:      "v1.29.0",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is 1.30.0 it should support native sidecars",
			gitVersion:      "v1.30.0",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is 1.28.0 it should not support native sidecars",
			gitVersion:      "v1.28.0",
			expectedSupport: false,
		},
		{
			name:            "When K8s version is 1.27.0 it should not support native sidecars",
			gitVersion:      "v1.27.0",
			expectedSupport: false,
		},
		{
			name:            "When K8s version is an OCP-style version it should parse correctly",
			gitVersion:      "v1.29.3+abcdef1",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is a GKE-style version it should support native sidecars",
			gitVersion:      "v1.29.0-gke.1",
			expectedSupport: true,
		},
		{
			name:            "When K8s version is a GKE-style version below 1.29 it should not support native sidecars",
			gitVersion:      "v1.28.0-gke.1",
			expectedSupport: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			client := fakeFailableDiscoveryClient{
				Resources:  []*metav1.APIResourceList{},
				gitVersion: tc.gitVersion,
			}

			caps, err := DetectManagementClusterCapabilities(client)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(caps.Has(CapabilityNativeSidecarContainers)).To(Equal(tc.expectedSupport))
		})
	}
}
