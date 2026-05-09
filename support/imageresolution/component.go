package imageresolution

type componentProvider struct {
	release       *ReleaseImage
	missingImages []string
}

func newComponentProvider(release *ReleaseImage) *componentProvider {
	return &componentProvider{release: release}
}

func (p *componentProvider) GetImage(name string) string {
	img, ok := p.release.ComponentImages[name]
	if !ok {
		p.missingImages = append(p.missingImages, name)
		return ""
	}
	return img
}

func (p *componentProvider) ImageExist(name string) (string, bool) {
	img, ok := p.release.ComponentImages[name]
	return img, ok
}

func (p *componentProvider) Version() string {
	return p.release.ComponentVersions["release"]
}

func (p *componentProvider) ComponentVersions() map[string]string {
	return cloneStringMap(p.release.ComponentVersions)
}

func (p *componentProvider) ComponentImages() map[string]string {
	return cloneStringMap(p.release.ComponentImages)
}
