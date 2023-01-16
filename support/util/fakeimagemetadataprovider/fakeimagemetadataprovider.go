package fakeimagemetadataprovider

import (
	"context"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
)

type FakeImageMetadataProvider struct {
	Result *dockerv1client.DockerImageConfig
}

func (f *FakeImageMetadataProvider) ImageMetadata(context.Context, string, []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.Result, nil
}
