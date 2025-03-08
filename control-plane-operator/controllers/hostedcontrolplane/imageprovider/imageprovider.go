package imageprovider

import "github.com/openshift/hypershift/support/releaseinfo"

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
