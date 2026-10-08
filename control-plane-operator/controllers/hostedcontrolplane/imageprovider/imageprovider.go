package imageprovider

import (
	"maps"

	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util/registryoverride"
)

//go:generate ../../../../hack/tools/bin/mockgen -source=imageprovider.go -package=imageprovider -destination=imageprovider_mock.go

// ReleaseImageProvider provides the functionality to retrieve OpenShift components' container image from a release image.
type ReleaseImageProvider interface {
	GetImage(key string) string
	ImageExist(key string) (string, bool)
	Version() string
	ComponentVersions() (map[string]string, error)
	ComponentImages() map[string]string
}

// ComponentImageOverrideProvider reports whether a component image was replaced
// independently of the selected release payload.
type ComponentImageOverrideProvider interface {
	ImageOverridden(key string) bool
}

var _ ReleaseImageProvider = &SimpleReleaseImageProvider{}
var _ ComponentImageOverrideProvider = &SimpleReleaseImageProvider{}

type SimpleReleaseImageProvider struct {
	missingImages    []string
	componentsImages map[string]string

	*releaseinfo.ReleaseImage
}

func New(releaseImage *releaseinfo.ReleaseImage) *SimpleReleaseImageProvider {
	return NewWithRegistryOverrides(releaseImage, nil)
}

func NewFromImages(componentsImages map[string]string) *SimpleReleaseImageProvider {
	return &SimpleReleaseImageProvider{
		componentsImages: componentsImages,
		missingImages:    make([]string, 0),
	}
}

func (p *SimpleReleaseImageProvider) GetImage(key string) string {
	image, exist := p.componentsImages[key]
	if !exist || image == "" {
		p.missingImages = append(p.missingImages, key)
	}

	return image
}

func (p *SimpleReleaseImageProvider) GetMissingImages() []string {
	return p.missingImages
}

func (p *SimpleReleaseImageProvider) ImageExist(key string) (string, bool) {
	img, exist := p.componentsImages[key]
	return img, exist
}

func (p *SimpleReleaseImageProvider) ComponentImages() map[string]string {
	return p.componentsImages
}

func (p *SimpleReleaseImageProvider) ImageOverridden(key string) bool {
	return p.ReleaseImage != nil && p.ReleaseImage.ComponentImageOverridden(key)
}

// ImageOverridden conservatively treats providers without override metadata as
// overridden, so callers do not infer binary capabilities from payload metadata
// when the selected component image is unknown.
func ImageOverridden(provider ReleaseImageProvider, key string) bool {
	overrideProvider, ok := provider.(ComponentImageOverrideProvider)
	return !ok || overrideProvider.ImageOverridden(key)
}

// NewWithRegistryOverrides creates a SimpleReleaseImageProvider that applies
// registry overrides to all component images. This ensures init containers
// and other sub-resources created by CPO use the overridden image references.
//
// The returned provider owns a private copy of releaseImage.ComponentImages()
// so callers can safely mutate it.
func NewWithRegistryOverrides(releaseImage *releaseinfo.ReleaseImage, registryOverrides map[string]string) *SimpleReleaseImageProvider {
	images := maps.Clone(releaseImage.ComponentImages())
	for key, image := range images {
		images[key] = registryoverride.Replace(image, registryOverrides)
	}
	return &SimpleReleaseImageProvider{
		componentsImages: images,
		missingImages:    make([]string, 0),
		ReleaseImage:     releaseImage,
	}
}
