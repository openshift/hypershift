package testutil

import "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

var _ imageprovider.ReleaseImageProvider = &fakeImageProvider{}

func FakeImageProvider(opts ...FakeImageProviderOpt) imageprovider.ReleaseImageProvider {
	f := &fakeImageProvider{
		version: "4.18",
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

type FakeImageProviderOpt func(*fakeImageProvider)

func WithVersion(version string) FakeImageProviderOpt {
	return func(f *fakeImageProvider) {
		f.version = version
	}
}

// WithImages sets the component images the fake provider reports.
func WithImages(images map[string]string) FakeImageProviderOpt {
	return func(f *fakeImageProvider) {
		f.images = images
	}
}

// WithComponentVersions sets the component versions the fake provider reports.
func WithComponentVersions(componentVersions map[string]string) FakeImageProviderOpt {
	return func(f *fakeImageProvider) {
		f.componentVersions = componentVersions
	}
}

type fakeImageProvider struct {
	version           string
	images            map[string]string
	componentVersions map[string]string
}

// ComponentVersions implements imageprovider.ReleaseImageProvider.
func (f *fakeImageProvider) ComponentVersions() (map[string]string, error) {
	if f.componentVersions != nil {
		return f.componentVersions, nil
	}
	return map[string]string{
		"kubernetes": "1.30",
	}, nil
}

// Version implements imageprovider.ReleaseImageProvider.
func (f *fakeImageProvider) Version() string {
	return f.version
}

func (f *fakeImageProvider) GetImage(key string) string {
	if f.images != nil {
		if img, ok := f.images[key]; ok {
			return img
		}
	}
	return key
}

func (f *fakeImageProvider) ImageExist(key string) (string, bool) {
	if f.images != nil {
		img, ok := f.images[key]
		return img, ok
	}
	return key, true
}
