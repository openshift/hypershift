package imageprovider

import "github.com/openshift/hypershift/support/releaseinfo"

type ReleaseImageProvider struct {
	missingImages    []string
	componentsImages map[string]string

	*releaseinfo.ReleaseImage
}

func New(releaseImage *releaseinfo.ReleaseImage) *ReleaseImageProvider {
	return &ReleaseImageProvider{
		componentsImages: releaseImage.ComponentImages(),
		missingImages:    make([]string, 0),
		ReleaseImage:     releaseImage,
	}
}

func NewFromImages(componentsImages map[string]string) *ReleaseImageProvider {
	return &ReleaseImageProvider{
		componentsImages: componentsImages,
		missingImages:    make([]string, 0),
	}
}

func (p *ReleaseImageProvider) GetImage(key string) string {
	image, exist := p.componentsImages[key]
	if !exist || image == "" {
		p.missingImages = append(p.missingImages, key)
	}

	return image
}

func (p *ReleaseImageProvider) GetMissingImages() []string {
	return p.missingImages
}

func (p *ReleaseImageProvider) ImageExist(key string) (string, bool) {
	img, exist := p.componentsImages[key]
	return img, exist
}
