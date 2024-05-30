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

// GetImage returns the image for the given key. If the image is not found, it will be added to the missing images list.
func (p *ReleaseImageProvider) GetImage(key string) string {
	image, exist := p.componentsImages[key]
	if !exist || image == "" {
		p.missingImages = append(p.missingImages, key)
	}

	return image
}

// GetImages returns the first image from the given key list. If the image is not found among the list,
// it will be added to the missing images list.
// This is useful when a component image has been renamed and we need to support multiple names among different OCP payloads.
// Sample: cluster-config-operator -> cluster-config-api
func (p *ReleaseImageProvider) GetImages(keys []string) string {
	var (
		exist bool
		image string
	)

	for _, imageName := range keys {
		image, exist = p.componentsImages[imageName]
		if exist && image != "" {
			break
		}
	}

	if !exist || image == "" {
		p.missingImages = append(p.missingImages, keys...)
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
