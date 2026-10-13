package add

import (
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"

	"github.com/docker/distribution"
)

func AddLayerToConfig(config *dockerv1client.DockerImageConfig, layer distribution.Descriptor, diffID string) {
	if config.RootFS == nil {
		config.RootFS = &dockerv1client.DockerConfigRootFS{Type: "layers"}
	}
	config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, diffID)
	config.Size += layer.Size
}
