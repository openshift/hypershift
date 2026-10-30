package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsscheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

func TestWaitForKarpenterCRDs(t *testing.T) {
	awsCRDNames := []string{
		"nodepools.karpenter.sh",
		"nodeclaims.karpenter.sh",
		"openshiftec2nodeclasses.karpenter.hypershift.openshift.io",
	}
	azureCRDNames := []string{
		"nodepools.karpenter.sh",
		"nodeclaims.karpenter.sh",
		"openshiftazurenodeclasses.karpenter.hypershift.openshift.io",
	}
	testCases := map[string]struct {
		platform         hyperv1.PlatformType
		crdNames         []string
		missingCRD       string
		unestablishedCRD string
		expectedErr      error
	}{
		"When AWS CRDs are established, it should become ready without Azure CRDs": {
			platform: hyperv1.AWSPlatform,
			crdNames: awsCRDNames,
		},
		"When Azure CRDs are established, it should become ready without AWS or AKS CRDs": {
			platform: hyperv1.AzurePlatform,
			crdNames: azureCRDNames,
		},
		"When an AWS CRD is missing, it should stop at the caller deadline": {
			platform:    hyperv1.AWSPlatform,
			crdNames:    awsCRDNames,
			missingCRD:  "openshiftec2nodeclasses.karpenter.hypershift.openshift.io",
			expectedErr: context.DeadlineExceeded,
		},
		"When an Azure CRD is missing, it should stop at the caller deadline": {
			platform:    hyperv1.AzurePlatform,
			crdNames:    azureCRDNames,
			missingCRD:  "openshiftazurenodeclasses.karpenter.hypershift.openshift.io",
			expectedErr: context.DeadlineExceeded,
		},
		"When an AWS CRD is not established, it should stop at the caller deadline": {
			platform:         hyperv1.AWSPlatform,
			crdNames:         awsCRDNames,
			unestablishedCRD: "openshiftec2nodeclasses.karpenter.hypershift.openshift.io",
			expectedErr:      context.DeadlineExceeded,
		},
		"When an Azure CRD is not established, it should stop at the caller deadline": {
			platform:         hyperv1.AzurePlatform,
			crdNames:         azureCRDNames,
			unestablishedCRD: "openshiftazurenodeclasses.karpenter.hypershift.openshift.io",
			expectedErr:      context.DeadlineExceeded,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			expectedCRDs := sets.New(tc.crdNames...)
			requestedCRDs := sets.New[string]()
			var requestsMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				const pathPrefix = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/"
				crdName := strings.TrimPrefix(r.URL.Path, pathPrefix)
				if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, pathPrefix) || !expectedCRDs.Has(crdName) {
					t.Errorf("unexpected CRD request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				requestsMu.Lock()
				requestedCRDs.Insert(crdName)
				requestsMu.Unlock()

				if crdName == tc.missingCRD {
					http.NotFound(w, r)
					return
				}
				crd := standaloneKarpenterCRD(crdName)
				if crdName == tc.unestablishedCRD {
					crd.Status.Conditions[0].Status = apiextensionsv1.ConditionFalse
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(crd); err != nil {
					t.Errorf("failed to write CRD response: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := waitForKarpenterCRDs(ctx, crdTestConfig(server.URL), tc.platform)
			if tc.expectedErr != nil {
				g.Expect(err).To(MatchError(tc.expectedErr))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			requestsMu.Lock()
			requested := requestedCRDs.Clone()
			requestsMu.Unlock()
			g.Expect(requested).To(Equal(expectedCRDs))
		})
	}
}

func TestWaitForNodeClassGVK(t *testing.T) {
	group := "karpenter.hypershift.openshift.io"
	awsResource := metav1.APIResource{Name: "openshiftec2nodeclasses", Kind: "OpenshiftEC2NodeClass"}
	azureResource := metav1.APIResource{Name: "openshiftazurenodeclasses", Kind: "OpenShiftAzureNodeClass"}
	testCases := map[string]struct {
		kind             string
		versions         []string
		preferredVersion string
		resources        map[string][]metav1.APIResource
		expectedVersion  string
	}{
		"When AWS serves only a different version, it should select that version": {
			kind:             awsResource.Kind,
			versions:         []string{"v1beta1"},
			preferredVersion: "v1beta1",
			resources:        map[string][]metav1.APIResource{"v1beta1": {awsResource}},
			expectedVersion:  "v1beta1",
		},
		"When Azure serves only a different version, it should select that version": {
			kind:             azureResource.Kind,
			versions:         []string{"v1beta1"},
			preferredVersion: "v1beta1",
			resources:        map[string][]metav1.APIResource{"v1beta1": {azureResource}},
			expectedVersion:  "v1beta1",
		},
		"When multiple versions are served, it should select the server preference rather than the first or newest version": {
			kind:             awsResource.Kind,
			versions:         []string{"v2alpha1", "v1"},
			preferredVersion: "v1",
			resources:        map[string][]metav1.APIResource{"v2alpha1": {awsResource}, "v1": {awsResource}},
			expectedVersion:  "v1",
		},
		"When the preferred group version lacks Azure, it should select the version serving Azure": {
			kind:             azureResource.Kind,
			versions:         []string{"v1", "v1alpha1"},
			preferredVersion: "v1",
			resources:        map[string][]metav1.APIResource{"v1": {awsResource}, "v1alpha1": {azureResource}},
			expectedVersion:  "v1alpha1",
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			apiGroup := metav1.APIGroup{
				Name:             group,
				PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: group + "/" + tc.preferredVersion, Version: tc.preferredVersion},
			}
			for _, version := range tc.versions {
				apiGroup.Versions = append(apiGroup.Versions, metav1.GroupVersionForDiscovery{GroupVersion: group + "/" + version, Version: version})
			}
			mapper := restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{{Group: apiGroup, VersionedResources: tc.resources}})
			gvk, err := waitForNodeClassGVK(t.Context(), mapper, schema.GroupKind{Group: group, Kind: tc.kind})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(gvk).To(Equal(schema.GroupVersionKind{Group: group, Version: tc.expectedVersion, Kind: tc.kind}))
		})
	}

	t.Run("When discovery fails, it should honor retry and termination rules", func(t *testing.T) {
		groupKind := schema.GroupKind{Group: "karpenter.hypershift.openshift.io", Kind: "OpenshiftEC2NodeClass"}
		gvk := groupKind.WithVersion("v1")
		noMatch := &meta.NoKindMatchError{GroupKind: groupKind}
		forbidden := apierrors.NewForbidden(schema.GroupResource{Group: groupKind.Group, Resource: "openshiftec2nodeclasses"}, "", fmt.Errorf("access denied"))
		testCases := map[string]struct {
			discoveryError error
			keepFailing    bool
			cancel         bool
			expectedError  error
			expectedCalls  int
		}{
			"When discovery initially has no match, it should retry until the API appears": {
				discoveryError: noMatch,
				expectedCalls:  2,
			},
			"When discovery initially returns not found, it should retry until the API appears": {
				discoveryError: apierrors.NewNotFound(schema.GroupResource{Group: groupKind.Group, Resource: "openshiftec2nodeclasses"}, ""),
				expectedCalls:  2,
			},
			"When discovery initially fails transiently, it should retry until discovery succeeds": {
				discoveryError: apierrors.NewServiceUnavailable("temporarily unavailable"),
				expectedCalls:  2,
			},
			"When discovery is forbidden, it should fail without retrying": {
				discoveryError: forbidden,
				expectedError:  forbidden,
				expectedCalls:  1,
			},
			"When the API remains missing, it should stop at the caller deadline": {
				discoveryError: noMatch,
				keepFailing:    true,
				expectedError:  context.DeadlineExceeded,
			},
			"When startup is canceled, it should stop without discovery requests": {
				cancel:        true,
				expectedError: context.Canceled,
				expectedCalls: 0,
			},
		}
		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				g := NewWithT(t)
				timeout := time.Second
				if tc.expectedError == nil {
					timeout = 3 * time.Second
				}
				ctx, cancel := context.WithTimeout(t.Context(), timeout)
				defer cancel()
				if tc.cancel {
					cancel()
				}
				calls := 0
				mapper := &nodeClassRESTMapperStub{mapping: func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
					g.Expect(gk).To(Equal(groupKind))
					g.Expect(versions).To(BeEmpty())
					calls++
					if calls == 1 || tc.keepFailing {
						return nil, tc.discoveryError
					}
					return &meta.RESTMapping{GroupVersionKind: gvk}, nil
				}}
				start := time.Now()
				actualGVK, err := waitForNodeClassGVK(ctx, mapper, groupKind)
				if tc.expectedError != nil {
					g.Expect(err).To(MatchError(tc.expectedError))
					g.Expect(actualGVK).To(Equal(schema.GroupVersionKind{}))
				} else {
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(actualGVK).To(Equal(gvk))
					g.Expect(time.Since(start)).To(BeNumerically(">=", time.Second))
				}
				if tc.keepFailing {
					g.Expect(calls).To(BeNumerically(">=", 1))
				} else {
					g.Expect(calls).To(Equal(tc.expectedCalls))
				}
			})
		}
	})
}

type nodeClassRESTMapperStub struct {
	meta.RESTMapper
	mapping func(schema.GroupKind, ...string) (*meta.RESTMapping, error)
}

func (m *nodeClassRESTMapperStub) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	return m.mapping(gk, versions...)
}

func standaloneKarpenterCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			Conditions: []apiextensionsv1.CustomResourceDefinitionCondition{
				{Type: apiextensionsv1.Established, Status: apiextensionsv1.ConditionTrue},
			},
		},
	}
}

func crdTestConfig(serverURL string) *rest.Config {
	return &rest.Config{
		Host:    serverURL,
		APIPath: "/apis",
		QPS:     -1, // Disable throttling so polling stops at the caller's deadline.
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1"},
			NegotiatedSerializer: apiextensionsscheme.Codecs.WithoutConversion(),
		},
	}
}
