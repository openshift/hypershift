package util

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/docker/distribution"
)

// GetMetadataFn is a function type for checking image availability
// It takes a context, image reference, and pull secret, and returns image metadata or an error
type GetMetadataFn func(context.Context, string, []byte) ([]distribution.Descriptor, distribution.BlobStore, error)

// LookupMappedImage looks up a mapped image based on OCP overrides
// by allowing tests to control the image availability check without reaching out to actual registries
func LookupMappedImage(ctx context.Context, ocpOverrides map[string][]string, image string, pullSecretBytes []byte, getMetadataFn GetMetadataFn) (string, error) {
	log := ctrl.LoggerFrom(ctx)
	ref, err := reference.Parse(image)
	if err != nil {
		return "", fmt.Errorf("failed to parse image (%s): %w", image, err)
	}

	for source, replacements := range ocpOverrides {
		if ref.AsRepository().String() == source {
			for _, replacement := range replacements {
				var newRef string
				// Handle both digest and tag formats
				if ref.ID != "" {
					// If image has digest, use it
					newRef = fmt.Sprintf("%s@%s", replacement, ref.ID)
				} else if ref.Tag != "" {
					// If image has tag, use it
					newRef = fmt.Sprintf("%s:%s", replacement, ref.Tag)
				} else {
					// Fallback to original image if neither digest nor tag is available
					log.Info("WARNING: Image reference has neither digest nor tag, skipping", "image", image)
					continue
				}

				// Verify mirror image availability using the provided function
				if _, _, err = getMetadataFn(ctx, newRef, pullSecretBytes); err == nil {
					return newRef, nil
				}
				log.Info("WARNING: The current mirrors image is unavailable, continue Scanning multiple mirrors", "error", err.Error(), "mirror image", ref)
				continue
			}
		}
	}
	return image, nil
}
