package openshiftmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"

	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/stretchr/testify/require"
)

func TestCheckSupportedInputResources(t *testing.T) {
	wellKnownExactResourceID := libraryinputresources.ExactResourceID{
		InputResourceTypeIdentifier: libraryinputresources.InputResourceTypeIdentifier{
			Group:    "example.io",
			Version:  "v1",
			Resource: "widgets",
		},
		Namespace: "default",
		Name:      "widget-a",
	}

	scenarios := []struct {
		name             string
		inputResources   map[string]*libraryinputresources.InputResources
		expectedErrorMsg string
	}{
		{
			name: "empty resources are supported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {},
			},
		},
		{
			name: "apply exact resources are supported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					ApplyConfigurationResources: libraryinputresources.ResourceList{
						ExactResources: []libraryinputresources.ExactResourceID{wellKnownExactResourceID},
					},
				},
			},
		},
		{
			name: "apply generated name resources are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					ApplyConfigurationResources: libraryinputresources.ResourceList{
						GeneratedNameResources: []libraryinputresources.GeneratedResourceID{
							{
								InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
								Namespace:                   "default",
								GeneratedName:               "widget-",
							},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: ApplyConfigurationResources.GeneratedNameResources are unsupported for now",
		},
		{
			name: "apply label selected resources are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					ApplyConfigurationResources: libraryinputresources.ResourceList{
						LabelSelectedResources: []libraryinputresources.LabelSelectedResource{
							{
								InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
								Namespace:                   "default",
							},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: ApplyConfigurationResources.LabelSelectedResources are unsupported for now",
		},
		{
			name: "apply resource references are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					ApplyConfigurationResources: libraryinputresources.ResourceList{
						ResourceReferences: []libraryinputresources.ResourceReference{
							{
								ReferringResource: wellKnownExactResourceID,
								Type:              libraryinputresources.ClusterScopedReferenceType,
								ClusterScopedReference: &libraryinputresources.ClusterScopedReference{
									InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
									NameJSONPath:                ".spec.ref",
								},
							},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: ApplyConfigurationResources.ResourceReferences are unsupported for now",
		},
		{
			name: "operand configuration exact resources are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					OperandResources: libraryinputresources.OperandResourceList{
						ConfigurationResources: libraryinputresources.ResourceList{
							ExactResources: []libraryinputresources.ExactResourceID{wellKnownExactResourceID},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: OperandResources.ConfigurationResources.ExactResources are unsupported for now",
		},
		{
			name: "operand management generated name resources are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					OperandResources: libraryinputresources.OperandResourceList{
						ManagementResources: libraryinputresources.ResourceList{
							GeneratedNameResources: []libraryinputresources.GeneratedResourceID{
								{
									InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
									Namespace:                   "default",
									GeneratedName:               "widget-",
								},
							},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: OperandResources.ManagementResources.GeneratedNameResources are unsupported for now",
		},
		{
			name: "operand user workload label selected resources are unsupported",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					OperandResources: libraryinputresources.OperandResourceList{
						UserWorkloadResources: libraryinputresources.ResourceList{
							LabelSelectedResources: []libraryinputresources.LabelSelectedResource{
								{
									InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
									Namespace:                   "default",
								},
							},
						},
					},
				},
			},
			expectedErrorMsg: "unsupported input resources found for operator-a operator: OperandResources.UserWorkloadResources.LabelSelectedResources are unsupported for now",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			initializer := &inputResourceInitializer{}
			err := initializer.checkSupportedInputResources(scenario.inputResources)

			if scenario.expectedErrorMsg == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.ErrorContains(t, err, scenario.expectedErrorMsg)
		})
	}
}

type fakeCache struct {
	cache.Cache
	getInformerForKindErr   error
	waitForCacheSyncResult  bool
	getInformerForKindCalls []schema.GroupVersionKind
}

func (f *fakeCache) GetInformerForKind(_ context.Context, gvk schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	f.getInformerForKindCalls = append(f.getInformerForKindCalls, gvk)
	return nil, f.getInformerForKindErr
}

func (f *fakeCache) WaitForCacheSync(_ context.Context) bool {
	return f.waitForCacheSyncResult
}

func fakeRESTMapperFor(gvk schema.GroupVersionKind, resource string) meta.RESTMapper {
	groupResources := []*restmapper.APIGroupResources{
		{
			Group: metav1.APIGroup{
				Name: gvk.Group,
				Versions: []metav1.GroupVersionForDiscovery{
					{GroupVersion: gvk.GroupVersion().String(), Version: gvk.Version},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: gvk.GroupVersion().String(),
					Version:      gvk.Version,
				},
			},
			VersionedResources: map[string][]metav1.APIResource{
				gvk.Version: {
					{
						Name:       resource,
						Kind:       gvk.Kind,
						Namespaced: true,
					},
				},
			},
		},
	}
	return restmapper.NewDiscoveryRESTMapper(groupResources)
}

func TestStartAndWaitForInformersFor(t *testing.T) {
	wellKnownExactResourceID := libraryinputresources.ExactResourceID{
		InputResourceTypeIdentifier: libraryinputresources.InputResourceTypeIdentifier{
			Group:    "example.io",
			Version:  "v1",
			Resource: "widgets",
		},
		Namespace: "default",
		Name:      "widget-a",
	}

	wellKnownGVK := schema.GroupVersionKind{Group: "example.io", Version: "v1", Kind: "Widget"}
	wellKnownGVR := schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}

	scenarios := []struct {
		name                   string
		inputResources         map[string]*libraryinputresources.InputResources
		fakeMapper             meta.RESTMapper
		getInformerForKindErr  error
		waitForCacheSyncResult bool
		expectedGVKs           []schema.GroupVersionKind
		expectedErr            string
	}{
		{
			name: "deduplicates gvk registrations",
			inputResources: map[string]*libraryinputresources.InputResources{
				"operator-a": {
					ApplyConfigurationResources: libraryinputresources.ResourceList{
						ExactResources: []libraryinputresources.ExactResourceID{
							wellKnownExactResourceID,
							{
								InputResourceTypeIdentifier: wellKnownExactResourceID.InputResourceTypeIdentifier,
								Namespace:                   "default",
								Name:                        "widget-b",
							},
						},
					},
				},
			},
			fakeMapper:             fakeRESTMapperFor(wellKnownGVK, wellKnownGVR.Resource),
			waitForCacheSyncResult: true,
			expectedGVKs:           []schema.GroupVersionKind{wellKnownGVK},
		},
		{
			name:                   "returns mapper error",
			inputResources:         map[string]*libraryinputresources.InputResources{"operator-a": {ApplyConfigurationResources: libraryinputresources.ResourceList{ExactResources: []libraryinputresources.ExactResourceID{wellKnownExactResourceID}}}},
			fakeMapper:             restmapper.NewDiscoveryRESTMapper(nil),
			waitForCacheSyncResult: false,
			expectedGVKs:           nil,
			expectedErr:            "unable to find Kind",
		},
		{
			name:                   "returns informer error",
			inputResources:         map[string]*libraryinputresources.InputResources{"operator-a": {ApplyConfigurationResources: libraryinputresources.ResourceList{ExactResources: []libraryinputresources.ExactResourceID{wellKnownExactResourceID}}}},
			fakeMapper:             fakeRESTMapperFor(wellKnownGVK, wellKnownGVR.Resource),
			getInformerForKindErr:  errors.New("cache error"),
			waitForCacheSyncResult: false,
			expectedGVKs:           []schema.GroupVersionKind{wellKnownGVK},
			expectedErr:            "cache error",
		},
		{
			name:                   "returns cache sync failure when not canceled",
			inputResources:         map[string]*libraryinputresources.InputResources{"operator-a": {ApplyConfigurationResources: libraryinputresources.ResourceList{ExactResources: []libraryinputresources.ExactResourceID{wellKnownExactResourceID}}}},
			fakeMapper:             fakeRESTMapperFor(wellKnownGVK, wellKnownGVR.Resource),
			waitForCacheSyncResult: false,
			expectedGVKs:           []schema.GroupVersionKind{wellKnownGVK},
			expectedErr:            "caches did not sync",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			fCache := &fakeCache{
				waitForCacheSyncResult: scenario.waitForCacheSyncResult,
				getInformerForKindErr:  scenario.getInformerForKindErr,
			}
			initializer := &inputResourceInitializer{
				managementClusterRESTMapper: scenario.fakeMapper,
				managementClusterCache:      fCache,
			}

			err := initializer.startAndWaitForInformersFor(context.Background(), scenario.inputResources)
			if scenario.expectedErr != "" {
				require.ErrorContains(t, err, scenario.expectedErr)
			} else {
				require.NoError(t, err)
			}
			require.ElementsMatch(t, scenario.expectedGVKs, fCache.getInformerForKindCalls)
		})
	}
}
