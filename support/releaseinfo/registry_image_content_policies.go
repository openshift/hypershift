package releaseinfo

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/docker/distribution"
)

var _ ProviderWithOpenShiftImageRegistryOverrides = (*ProviderWithOpenShiftImageRegistryOverridesDecorator)(nil)

type ProviderWithOpenShiftImageRegistryOverridesDecorator struct {
	Delegate ProviderWithRegistryOverrides
	// overrides holds an immutable snapshot published by SetOpenShiftImageRegistryOverrides.
	overrides            atomic.Pointer[map[string][]string]
	mirroredReleaseImage atomic.Pointer[string]

	// repoSetupFn is an injectable function for verifying mirror availability.
	// When nil, defaults to registryclient.GetRepoSetup.
	repoSetupFn func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error)

	lock sync.Mutex
}

// SetOpenShiftImageRegistryOverrides publishes the current image registry mirror snapshot.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) SetOpenShiftImageRegistryOverrides(overrides map[string][]string) {
	snapshot := CopyOpenShiftImageRegistryOverrides(overrides)
	p.overrides.Store(&snapshot)
}

// loadOverrides returns an immutable snapshot; callers must not modify it.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) loadOverrides() map[string][]string {
	if snapshot := p.overrides.Load(); snapshot != nil {
		return *snapshot
	}
	return nil
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	repoSetup := p.repoSetupFn
	if repoSetup == nil {
		repoSetup = registryclient.GetRepoSetup
	}

	overrides := p.loadOverrides()

	logger := ctrl.LoggerFrom(ctx)

	for registrySource, registryDest := range overrides {
		if strings.Contains(image, registrySource) {
			for _, registryReplacement := range registryDest {
				replacedImage := strings.Replace(image, registrySource, registryReplacement, 1)

				// Attempt to lookup image with mirror registry destination
				releaseImage, err := p.Delegate.Lookup(ctx, replacedImage, pullSecret)
				if releaseImage != nil {
					// Verify mirror image availability.
					if _, _, err = repoSetup(ctx, replacedImage, pullSecret); err == nil {
						p.mirroredReleaseImage.Store(&replacedImage)
						return releaseImage, nil
					}
					logger.Info("WARNING: The current mirrors image is unavailable, continue Scanning multiple mirrors", "error", err.Error(), "mirror image", image)
					continue
				}

				logger.Error(err, "Failed to look up release image using registry mirror", "registry mirror", registryReplacement)
			}
		}
	}

	// Reset mirrored release image when falling back to original
	p.mirroredReleaseImage.Store(nil)
	return p.Delegate.Lookup(ctx, image, pullSecret)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetRegistryOverrides() map[string]string {
	return p.Delegate.GetRegistryOverrides()
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetOpenShiftImageRegistryOverrides() map[string][]string {
	return CopyOpenShiftImageRegistryOverrides(p.loadOverrides())
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetMirroredReleaseImage() string {
	if image := p.mirroredReleaseImage.Load(); image != nil {
		return *image
	}
	return ""
}

// CopyOpenShiftImageRegistryOverrides copies the map and its mirror slices, preserving nil values.
func CopyOpenShiftImageRegistryOverrides(overrides map[string][]string) map[string][]string {
	if overrides == nil {
		return nil
	}

	copyOfOverrides := make(map[string][]string, len(overrides))
	for source, mirrors := range overrides {
		if mirrors == nil {
			copyOfOverrides[source] = nil
			continue
		}
		copyOfOverrides[source] = make([]string, len(mirrors))
		copy(copyOfOverrides[source], mirrors)
	}

	return copyOfOverrides
}
