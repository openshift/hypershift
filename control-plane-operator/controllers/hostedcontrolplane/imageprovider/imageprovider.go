package imageprovider

import (
	"strings"

	"github.com/openshift/hypershift/support/releaseinfo"
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

var _ ReleaseImageProvider = &SimpleReleaseImageProvider{}

type SimpleReleaseImageProvider struct {
	missingImages    []string
	componentsImages map[string]string

	*releaseinfo.ReleaseImage
}

func New(releaseImage *releaseinfo.ReleaseImage) *SimpleReleaseImageProvider {
	return &SimpleReleaseImageProvider{
		componentsImages: releaseImage.ComponentImages(),
		missingImages:    make([]string, 0),
		ReleaseImage:     releaseImage,
	}
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

// NewWithRegistryOverrides creates a SimpleReleaseImageProvider that applies
// registry overrides to all component images. This ensures init containers
// and other sub-resources created by CPO use the overridden image references.
func NewWithRegistryOverrides(releaseImage *releaseinfo.ReleaseImage, registryOverrides map[string]string) *SimpleReleaseImageProvider {
	originalImages := releaseImage.ComponentImages()
	images := make(map[string]string, len(originalImages))
	for k, v := range originalImages {
		images[k] = v
	}
	if len(registryOverrides) > 0 {
		for key, image := range images {
			for source, target := range registryOverrides {
				if image == source || strings.HasPrefix(image, source+"/") {
					images[key] = strings.Replace(image, source, target, 1)
					break
				}
			}
		}
	}
	return &SimpleReleaseImageProvider{
		componentsImages: images,
		missingImages:    make([]string, 0),
		ReleaseImage:     releaseImage,
	}
}
