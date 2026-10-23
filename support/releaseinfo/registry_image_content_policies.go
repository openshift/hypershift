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
	// OpenShiftImageRegistryOverrides is retained for struct-literal compatibility. Runtime updates must use SetOpenShiftImageRegistryOverrides.
	OpenShiftImageRegistryOverrides map[string][]string
	mirroredReleaseImage            string

	// repoSetupFn is an injectable function for verifying mirror availability.
	// When nil, defaults to registryclient.GetRepoSetup.
	repoSetupFn func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error)

	lock      sync.Mutex
	stateLock sync.RWMutex
}

// SetOpenShiftImageRegistryOverrides publishes the current image registry mirror snapshot.
func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) SetOpenShiftImageRegistryOverrides(overrides map[string][]string) {
	p.stateLock.Lock()
	defer p.stateLock.Unlock()

	p.OpenShiftImageRegistryOverrides = copyOpenShiftImageRegistryOverrides(overrides)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	repoSetup := p.repoSetupFn
	if repoSetup == nil {
		repoSetup = registryclient.GetRepoSetup
	}

	p.stateLock.RLock()
	overrides := copyOpenShiftImageRegistryOverrides(p.OpenShiftImageRegistryOverrides)
	p.stateLock.RUnlock()

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
						p.stateLock.Lock()
						p.mirroredReleaseImage = replacedImage
						p.stateLock.Unlock()
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
	p.stateLock.Lock()
	p.mirroredReleaseImage = ""
	p.stateLock.Unlock()
	return p.Delegate.Lookup(ctx, image, pullSecret)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetRegistryOverrides() map[string]string {
	return p.Delegate.GetRegistryOverrides()
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetOpenShiftImageRegistryOverrides() map[string][]string {
	p.stateLock.RLock()
	defer p.stateLock.RUnlock()

	return copyOpenShiftImageRegistryOverrides(p.OpenShiftImageRegistryOverrides)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetMirroredReleaseImage() string {
	p.stateLock.RLock()
	defer p.stateLock.RUnlock()

	return p.mirroredReleaseImage
}

func copyOpenShiftImageRegistryOverrides(overrides map[string][]string) map[string][]string {
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
