package imageresolution

import (
	"context"
	"crypto/tls"
	"maps"
	"net/http"
	"time"

	"github.com/openshift/hypershift/support/releaseinfo"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	hyperutil "github.com/openshift/hypershift/support/util"

	imageapi "github.com/openshift/api/image/v1"
)

type httpMirrorChecker struct {
	client *http.Client
}

func newHTTPMirrorChecker() *httpMirrorChecker {
	return &httpMirrorChecker{
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				// #nosec G402 -- mirrors may use self-signed certs; availability
				// probe only, no data is transferred.
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
					MinVersion:         tls.VersionTLS12,
				},
			},
		},
	}
}

func (h *httpMirrorChecker) isAvailable(ctx context.Context, registry string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+registry+"/v2/", nil)
	if err != nil {
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// 401/403 indicate the registry exists but requires auth — still "available".
	return resp.StatusCode == http.StatusOK ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden
}

type registryReleaseFetcher struct {
	delegate *releaseinfo.RegistryClientProvider
}

func newRegistryReleaseFetcher() releaseFetcher {
	return &registryReleaseFetcher{
		delegate: &releaseinfo.RegistryClientProvider{},
	}
}

func (f *registryReleaseFetcher) fetch(ctx context.Context, pullSpec string, pullSecret []byte) (*ReleaseImage, error) {
	ri, err := f.delegate.Lookup(ctx, pullSpec, pullSecret)
	if err != nil {
		return nil, err
	}
	return convertReleaseImage(ri), nil
}

// convertReleaseImage converts an external releaseinfo.ReleaseImage into the internal
// ReleaseImage type, extracting component images and versions from ImageStream tags
// and annotations. The ImageStream is deep-copied to prevent mutation of cached data.
func convertReleaseImage(ri *releaseinfo.ReleaseImage) *ReleaseImage {
	componentImages := make(map[string]string)
	if ri.ImageStream != nil {
		for _, tag := range ri.ImageStream.Spec.Tags {
			if tag.From != nil {
				componentImages[tag.Name] = tag.From.Name
			}
		}
	}

	componentVersions := make(map[string]string)
	if ri.ImageStream != nil {
		maps.Copy(componentVersions, ri.ImageStream.ObjectMeta.Annotations)
		if len(ri.ImageStream.Name) > 0 {
			componentVersions["release"] = ri.ImageStream.Name
		}
	}

	var is *imageapi.ImageStream
	if ri.ImageStream != nil {
		cp := ri.ImageStream.DeepCopy()
		is = cp
	}

	return &ReleaseImage{
		ComponentImages:   componentImages,
		ComponentVersions: componentVersions,
		ImageStream:       is,
		StreamMetadata:    ri.StreamMetadata,
	}
}

type registryMetadataFetcher struct {
	delegate *hyperutil.RegistryClientImageMetadataProvider
}

func newRegistryMetadataFetcher() imageMetadataFetcher {
	return &registryMetadataFetcher{
		delegate: &hyperutil.RegistryClientImageMetadataProvider{},
	}
}

func (f *registryMetadataFetcher) fetchConfig(ctx context.Context, ref string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.delegate.ImageMetadata(ctx, ref, pullSecret)
}
