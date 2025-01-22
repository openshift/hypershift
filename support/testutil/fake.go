package testutil

import "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"

var _ imageprovider.ReleaseImageProvider = &fakeImageProvider{}

func FakeImageProvider() imageprovider.ReleaseImageProvider {
	return &fakeImageProvider{}
}

type fakeImageProvider struct {
}

// ComponentVersions implements imageprovider.ReleaseImageProvider.
func (f *fakeImageProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{
		"kubernetes": "1.30.1",
	}, nil
}

// Version implements imageprovider.ReleaseImageProvider.
func (f *fakeImageProvider) Version() string {
	return "4.18.0"
}

func (f *fakeImageProvider) GetImage(key string) string {
	return key
}

func (f *fakeImageProvider) ImageExist(key string) (string, bool) {
	return key, true
}
