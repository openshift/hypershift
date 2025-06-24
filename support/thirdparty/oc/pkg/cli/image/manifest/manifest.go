package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	imagereference "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"

	"k8s.io/klog"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/manifest/ocischema"
	"github.com/docker/distribution/manifest/schema2"
	"github.com/opencontainers/go-digest"
	imagespecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

var ErrAllImagesFiltered = fmt.Errorf("filtered all images from manifest list")

// PreferManifestList specifically requests a manifest list first
var PreferManifestList = distribution.WithManifestMediaTypes([]string{
	manifestlist.MediaTypeManifestList,
	schema2.MediaTypeManifest,
	imagespecv1.MediaTypeImageManifest,
})

func PlatformSpecString(platform manifestlist.PlatformSpec) string {
	if len(platform.Variant) > 0 {
		return fmt.Sprintf("%s/%s/%s", platform.OS, platform.Architecture, platform.Variant)
	}
	return fmt.Sprintf("%s/%s", platform.OS, platform.Architecture)
}

// FilterOptions assist in filtering out unneeded manifests from ManifestList objects.
type FilterOptions struct {
	FilterByOS      string
	DefaultOSFilter bool
	OSFilter        *regexp.Regexp
}

// IsWildcardFilter returns true if the filter regex is set to a wildcard
func (o *FilterOptions) IsWildcardFilter() bool {
	wildcardFilter := ".*"
	return o.FilterByOS == wildcardFilter
}

// Include returns true if the provided manifest should be included, or the first image if the user didn't alter the
// default selection and there is only one image.
func (o *FilterOptions) Include(d *manifestlist.ManifestDescriptor, hasMultiple bool) bool {
	if o.OSFilter == nil {
		return true
	}
	if o.DefaultOSFilter && !hasMultiple {
		return true
	}
	s := PlatformSpecString(d.Platform)
	return o.OSFilter.MatchString(s)
}

// IncludeAll returns true if the provided manifest matches the filter, or all if there was no filter.
func (o *FilterOptions) IncludeAll(d *manifestlist.ManifestDescriptor, hasMultiple bool) bool {
	if o.OSFilter == nil {
		return true
	}
	s := PlatformSpecString(d.Platform)
	return o.OSFilter.MatchString(s)
}

type FilterFunc func(*manifestlist.ManifestDescriptor, bool) bool

type ManifestLocation struct {
	Manifest     digest.Digest
	ManifestList digest.Digest
}

func (m ManifestLocation) IsList() bool {
	return len(m.ManifestList) > 0
}

func (m ManifestLocation) String() string {
	if m.IsList() {
		return fmt.Sprintf("manifest %s in manifest list %s", m.Manifest, m.ManifestList)
	}
	return fmt.Sprintf("manifest %s", m.Manifest)
}

// FirstManifest returns the first manifest at the request location that matches the filter function.
func FirstManifest(ctx context.Context, from imagereference.DockerImageReference, repo distribution.Repository, filterFn FilterFunc) (distribution.Manifest, ManifestLocation, error) {
	var srcDigest digest.Digest
	if len(from.ID) > 0 {
		srcDigest = digest.Digest(from.ID)
	} else if len(from.Tag) > 0 {
		desc, err := repo.Tags(ctx).Get(ctx, from.Tag)
		if err != nil {
			return nil, ManifestLocation{}, err
		}
		srcDigest = desc.Digest
	} else {
		return nil, ManifestLocation{}, fmt.Errorf("no tag or digest specified")
	}
	manifests, err := repo.Manifests(ctx)
	if err != nil {
		return nil, ManifestLocation{}, err
	}
	srcManifest, err := manifests.Get(ctx, srcDigest, PreferManifestList)
	if err != nil {
		return nil, ManifestLocation{}, err
	}

	originalSrcDigest := srcDigest
	srcChildren, srcManifest, srcDigest, err := ProcessManifestList(ctx, srcDigest, srcManifest, manifests, from, filterFn, false)
	if err != nil {
		return nil, ManifestLocation{}, err
	}
	if srcManifest == nil {
		return nil, ManifestLocation{}, ErrAllImagesFiltered
	}
	if len(srcChildren) > 0 {
		// More than one match within the list, return the first
		childManifest := srcChildren[0]
		childDigest, err := registryclient.ContentDigestForManifest(childManifest, srcDigest.Algorithm())
		if err != nil {
			return nil, ManifestLocation{}, fmt.Errorf("could not generate digest for first manifest")
		}
		return childManifest, ManifestLocation{Manifest: childDigest, ManifestList: originalSrcDigest}, nil
	}
	if srcDigest != originalSrcDigest {
		// One match in list selected by ProcessManifestList
		return srcManifest, ManifestLocation{Manifest: srcDigest, ManifestList: originalSrcDigest}, nil
	}
	// Was not a list
	return srcManifest, ManifestLocation{Manifest: srcDigest}, nil
}

// ManifestToImageConfig takes an image manifest and converts it into a structured object.
func ManifestToImageConfig(ctx context.Context, srcManifest distribution.Manifest, blobs distribution.BlobService, location ManifestLocation) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, error) {
	switch t := srcManifest.(type) {
	case *schema2.DeserializedManifest:
		if t.Config.MediaType != schema2.MediaTypeImageConfig {
			return nil, nil, fmt.Errorf("%s does not have the expected image configuration media type: %s", location, t.Config.MediaType)
		}
		configJSON, err := blobs.Get(ctx, t.Config.Digest)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot retrieve image configuration for %s: %v", location, err)
		}
		klog.V(4).Infof("Raw image config json:\n%s", string(configJSON))
		config := &dockerv1client.DockerImageConfig{}
		if err := json.Unmarshal(configJSON, &config); err != nil {
			return nil, nil, fmt.Errorf("unable to parse image configuration: %v", err)
		}

		base := config
		layers := t.Layers
		base.Size = 0
		for _, layer := range t.Layers {
			base.Size += layer.Size
		}

		return base, layers, nil

	case *ocischema.DeserializedManifest:
		if t.Config.MediaType != imagespecv1.MediaTypeImageConfig {
			return nil, nil, fmt.Errorf("%s does not have the expected image configuration media type: %s", location, t.Config.MediaType)
		}
		configJSON, err := blobs.Get(ctx, t.Config.Digest)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot retrieve image configuration for %s: %v", location, err)
		}
		klog.V(4).Infof("Raw image config json:\n%s", string(configJSON))
		config := &dockerv1client.DockerImageConfig{}
		if err := json.Unmarshal(configJSON, &config); err != nil {
			return nil, nil, fmt.Errorf("unable to parse image configuration: %v", err)
		}

		base := config
		layers := t.Layers
		base.Size = 0
		for _, layer := range t.Layers {
			base.Size += layer.Size
		}

		return base, layers, nil
	case *manifestlist.DeserializedManifestList:
		return nil, nil, fmt.Errorf("use --keep-manifest-list option for image manifest type %T from %s", srcManifest, location)
	default:
		return nil, nil, fmt.Errorf("unknown image manifest of type %T from %s", srcManifest, location)
	}
}

func ProcessManifestList(ctx context.Context, srcDigest digest.Digest, srcManifest distribution.Manifest, manifests distribution.ManifestService, ref imagereference.DockerImageReference, filterFn FilterFunc, keepManifestList bool) ([]distribution.Manifest, distribution.Manifest, digest.Digest, error) {
	var childManifests []distribution.Manifest
	switch t := srcManifest.(type) {
	case *manifestlist.DeserializedManifestList:
		manifestDigest := srcDigest
		manifestList := t

		filtered := make([]manifestlist.ManifestDescriptor, 0, len(t.Manifests))
		for _, manifest := range t.Manifests {
			if !filterFn(&manifest, len(t.Manifests) > 1) {
				klog.V(5).Infof("Skipping image %s for %#v from %s", manifest.Digest, manifest.Platform, ref)
				continue
			}
			klog.V(5).Infof("Including image %s for %#v from %s", manifest.Digest, manifest.Platform, ref)
			filtered = append(filtered, manifest)
		}

		if len(filtered) == 0 && !keepManifestList {
			return nil, nil, "", nil
		}

		// if we're not keeping manifest lists and this one has been filtered, make a new one with
		// just the filtered platforms.
		if len(filtered) != len(t.Manifests) && !keepManifestList {
			var err error
			t, err = manifestlist.FromDescriptors(filtered)
			if err != nil {
				return nil, nil, "", fmt.Errorf("unable to filter source image %s manifest list: %v", ref, err)
			}
			_, body, err := t.Payload()
			if err != nil {
				return nil, nil, "", fmt.Errorf("unable to filter source image %s manifest list (bad payload): %v", ref, err)
			}
			manifestList = t
			manifestDigest, err = registryclient.ContentDigestForManifest(t, srcDigest.Algorithm())
			if err != nil {
				return nil, nil, "", err
			}
			klog.V(5).Infof("Filtered manifest list to new digest %s:\n%s", manifestDigest, body)
		}

		for i, manifest := range filtered {
			childManifest, err := manifests.Get(ctx, manifest.Digest, PreferManifestList)
			if err != nil {
				return nil, nil, "", fmt.Errorf("unable to retrieve source image %s manifest #%d from manifest list: %v", ref, i+1, err)
			}
			childManifests = append(childManifests, childManifest)
		}

		switch {
		case len(childManifests) == 1 && !keepManifestList:
			// Just return the single platform specific image
			manifestDigest, err := registryclient.ContentDigestForManifest(childManifests[0], srcDigest.Algorithm())
			if err != nil {
				return nil, nil, "", err
			}
			klog.V(2).Infof("Chose %s/%s manifest from the manifest list.", t.Manifests[0].Platform.OS, t.Manifests[0].Platform.Architecture)
			return nil, childManifests[0], manifestDigest, nil
		default:
			return childManifests, manifestList, manifestDigest, nil
		}

	default:
		return nil, srcManifest, srcDigest, nil
	}
}
