package releaseinfo

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
)

const (
	ReleaseImageStreamFile   = "release-manifests/image-references"
	ReleaseImageMetadataFile = "release-manifests/0000_50_installer_coreos-bootimages.yaml"
)

var _ Provider = (*RegistryClientProvider)(nil)

// RegistryClientProvider uses a registry client to directly stream image
// content and extract image metadata.
type RegistryClientProvider struct {
}

func (p *RegistryClientProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (releaseImage *ReleaseImage, err error) {
	fileContents, err := registryclient.ExtractImageFiles(ctx, image, pullSecret, ReleaseImageStreamFile, ReleaseImageMetadataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to extract release metadata: %w", err)
	}

	if _, ok := fileContents[ReleaseImageStreamFile]; !ok {
		return nil, fmt.Errorf("release image references file not found in release image %s", image)
	}
	imageStream, err := DeserializeImageStream(fileContents[ReleaseImageStreamFile])
	if err != nil {
		return nil, err
	}

	if _, ok := fileContents[ReleaseImageMetadataFile]; !ok {
		return nil, fmt.Errorf("release image metadata file not found in release image %s", image)
	}
	coreOSMeta, err := DeserializeImageMetadata(fileContents[ReleaseImageMetadataFile])
	if err != nil {
		return nil, err
	}

	// Parse multi-stream metadata if present (5.0+ payloads).
	// Errors are non-fatal since the legacy single-stream is sufficient for < 5.0.
	streamsMeta, _ := DeserializeMultiStreamImageMetadata(fileContents[ReleaseImageMetadataFile])

	return &ReleaseImage{
		ImageStream:     imageStream,
		StreamMetadata:  coreOSMeta,
		StreamsMetadata: streamsMeta,
	}, nil
}
