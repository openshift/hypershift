package manifestclient

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	apidiscoveryv2 "k8s.io/api/apidiscovery/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/yaml"
)

type kindData struct {
	kind     schema.GroupVersionKind
	listKind schema.GroupVersionKind
	err      error
}

func newDiscoveryReader(content fs.FS) *discoveryReader {
	return &discoveryReader{
		sourceFS:        content,
		kindForResource: make(map[schema.GroupVersionResource]kindData),
	}
}

type discoveryReader struct {
	kindForResource map[schema.GroupVersionResource]kindData

	sourceFS fs.FS
	lock     sync.RWMutex
}

func (dr *discoveryReader) getKindForResource(gvr schema.GroupVersionResource) (kindData, error) {
	dr.lock.RLock()
	kindForGVR, ok := dr.kindForResource[gvr]
	if ok {
		defer dr.lock.RUnlock()
		return kindForGVR, kindForGVR.err
	}
	dr.lock.RUnlock()

	dr.lock.Lock()
	defer dr.lock.Unlock()

	kindForGVR, ok = dr.kindForResource[gvr]
	if ok {
		return kindForGVR, kindForGVR.err
	}

	discoveryPath := "/apis"
	if len(gvr.Group) == 0 {
		discoveryPath = "/api"
	}
	discoveryBytes, err := dr.getGroupResourceDiscovery(&apirequest.RequestInfo{Path: discoveryPath})
	if err != nil {
		kindForGVR.err = fmt.Errorf("error reading discovery: %w", err)
		dr.kindForResource[gvr] = kindForGVR
		return kindForGVR, kindForGVR.err
	}

	discoveryInfo := &apidiscoveryv2.APIGroupDiscoveryList{}
	if err := json.Unmarshal(discoveryBytes, discoveryInfo); err != nil {
		kindForGVR.err = fmt.Errorf("error unmarshalling discovery: %w", err)
		dr.kindForResource[gvr] = kindForGVR
		return kindForGVR, kindForGVR.err
	}

	kindForGVR.err = fmt.Errorf("did not find kind for %v\n", gvr)
	for _, groupInfo := range discoveryInfo.Items {
		if groupInfo.Name != gvr.Group {
			continue
		}
		for _, versionInfo := range groupInfo.Versions {
			if versionInfo.Version != gvr.Version {
				continue
			}
			for _, resourceInfo := range versionInfo.Resources {
				if resourceInfo.Resource != gvr.Resource {
					continue
				}
				if resourceInfo.ResponseKind == nil {
					continue
				}
				kindForGVR.kind = schema.GroupVersionKind{
					Group:   gvr.Group,
					Version: gvr.Version,
					Kind:    resourceInfo.ResponseKind.Kind,
				}
				if len(resourceInfo.ResponseKind.Group) > 0 {
					kindForGVR.kind.Group = resourceInfo.ResponseKind.Group
				}
				if len(resourceInfo.ResponseKind.Version) > 0 {
					kindForGVR.kind.Version = resourceInfo.ResponseKind.Version
				}
				kindForGVR.listKind = schema.GroupVersionKind{
					Group:   kindForGVR.kind.Group,
					Version: kindForGVR.kind.Version,
					Kind:    resourceInfo.ResponseKind.Kind + "List",
				}
				kindForGVR.err = nil
				dr.kindForResource[gvr] = kindForGVR
				return kindForGVR, kindForGVR.err
			}
		}
	}

	dr.kindForResource[gvr] = kindForGVR
	return kindForGVR, kindForGVR.err
}

func (dr *discoveryReader) getGroupResourceDiscovery(requestInfo *apirequest.RequestInfo) ([]byte, error) {
	switch {
	case requestInfo.Path == "/api":
		return dr.getAggregatedDiscoveryForURL("aggregated-discovery-api.yaml", requestInfo.Path)
	case requestInfo.Path == "/apis":
		return dr.getAggregatedDiscoveryForURL("aggregated-discovery-apis.yaml", requestInfo.Path)
	default:
		// TODO can probably do better
		return nil, fmt.Errorf("unsupported discovery path: %q", requestInfo.Path)
	}
}

func (dr *discoveryReader) getAggregatedDiscoveryForURL(filename, url string) ([]byte, error) {
	discoveryBytes, err := fs.ReadFile(dr.sourceFS, filename)
	if errors.Is(err, fs.ErrNotExist) {
		discoveryBytes, err = fs.ReadFile(defaultDiscovery, filepath.Join("default-discovery", filename))
	}
	if err != nil {
		return nil, fmt.Errorf("error reading discovery: %w", err)
	}

	apiMap := map[string]interface{}{}
	if err := yaml.Unmarshal(discoveryBytes, &apiMap); err != nil {
		return nil, fmt.Errorf("discovery %q unmarshal failed: %w", url, err)
	}
	apiJSON, err := json.Marshal(apiMap)
	if err != nil {
		return nil, fmt.Errorf("discovery %q marshal failed: %w", url, err)
	}

	return apiJSON, err
}

//go:embed default-discovery
var defaultDiscovery embed.FS
