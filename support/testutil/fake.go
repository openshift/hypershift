package testutil

import "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

var _ imageprovider.ReleaseImageProvider = &fakeImageProvider{}

func FakeImageProvider(opts ...FakeImageProviderOpt) imageprovider.ReleaseImageProvider {
	f := &fakeImageProvider{
		version: "4.18.0",
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

func WithImages(images map[string]string) FakeImageProviderOpt {
	return func(f *fakeImageProvider) {
		f.images = images
	}
}

type fakeImageProvider struct {
	version string
	images  map[string]string
}

// ComponentVersions implements imageprovider.ReleaseImageProvider.
func (f *fakeImageProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{
		"kubernetes": "1.30.1",
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

func (f *fakeImageProvider) ComponentImages() map[string]string {
	if f.images != nil {
		return f.images
	}
	return map[string]string{}
}
