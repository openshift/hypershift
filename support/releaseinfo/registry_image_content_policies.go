package releaseinfo

import (
	"context"
	"strings"
	"sync"

	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/docker/distribution"
)

var _ ProviderWithOpenShiftImageRegistryOverrides = (*ProviderWithOpenShiftImageRegistryOverridesDecorator)(nil)

type ProviderWithOpenShiftImageRegistryOverridesDecorator struct {
	Delegate ProviderWithRegistryOverrides

	// repoSetupFn is an injectable function for verifying mirror availability.
	// When nil, defaults to registryclient.GetRepoSetup.
	repoSetupFn func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error)

	// overridesLock protects openShiftImageRegistryOverrides. It is separate
	// from lookupLock so that Reconcile (which calls Set) does not block
	// behind a slow Lookup doing network calls.
	overridesLock                   sync.RWMutex
	openShiftImageRegistryOverrides map[string][]string

	// lookupLock serializes Lookup calls and protects mirroredReleaseImage.
	lookupLock           sync.Mutex
	mirroredReleaseImage string
}

// SetOpenShiftImageRegistryOverrides replaces the mirror map under a write lock.
// The provided map is deep-copied so callers cannot mutate provider state.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) SetOpenShiftImageRegistryOverrides(overrides map[string][]string) {
	p.overridesLock.Lock()
	defer p.overridesLock.Unlock()
	p.openShiftImageRegistryOverrides = deepCopyMirrorMap(overrides)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lookupLock.Lock()
	defer p.lookupLock.Unlock()

	// Snapshot overrides under the read lock so Reconcile is not blocked
	// behind a slow registry call.
	p.overridesLock.RLock()
	overrides := deepCopyMirrorMap(p.openShiftImageRegistryOverrides)
	p.overridesLock.RUnlock()

	repoSetup := p.repoSetupFn
	if repoSetup == nil {
		repoSetup = registryclient.GetRepoSetup
	}

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
						p.mirroredReleaseImage = replacedImage
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
	p.mirroredReleaseImage = ""
	return p.Delegate.Lookup(ctx, image, pullSecret)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetRegistryOverrides() map[string]string {
	return p.Delegate.GetRegistryOverrides()
}

// GetOpenShiftImageRegistryOverrides returns a deep copy of the mirror map under a read lock.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetOpenShiftImageRegistryOverrides() map[string][]string {
	p.overridesLock.RLock()
	defer p.overridesLock.RUnlock()
	return deepCopyMirrorMap(p.openShiftImageRegistryOverrides)
}

// GetMirroredReleaseImage returns the currently mirrored release image.
// It is protected by lookupLock because only Lookup writes to it.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetMirroredReleaseImage() string {
	p.lookupLock.Lock()
	defer p.lookupLock.Unlock()
	return p.mirroredReleaseImage
}

// deepCopyMirrorMap returns a deep copy of a map[string][]string.
// Returns nil when the input is nil, preserving nil vs empty semantics.
func deepCopyMirrorMap(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
