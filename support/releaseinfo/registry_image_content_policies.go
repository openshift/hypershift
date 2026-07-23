package releaseinfo

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/docker/distribution"
)

var _ ProviderWithOpenShiftImageRegistryOverrides = (*ProviderWithOpenShiftImageRegistryOverridesDecorator)(nil)

type ProviderWithOpenShiftImageRegistryOverridesDecorator struct {
	Delegate                        ProviderWithRegistryOverrides
	OpenShiftImageRegistryOverrides map[string][]string
	mirroredReleaseImage            string

	// repoSetupFn is an injectable function for verifying mirror availability.
	// When nil, defaults to registryclient.GetRepoSetup.
	repoSetupFn func(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error)

	lock sync.Mutex
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	start := time.Now()
	p.lock.Lock()
	defer p.lock.Unlock()
	lockWaitDuration.Observe(time.Since(start).Seconds())
	defer func() { lookupDuration.Observe(time.Since(start).Seconds()) }()

	repoSetup := p.repoSetupFn
	if repoSetup == nil {
		repoSetup = registryclient.GetRepoSetup
	}

	logger := ctrl.LoggerFrom(ctx)

	for registrySource, registryDest := range p.OpenShiftImageRegistryOverrides {
		if strings.Contains(image, registrySource) {
			for _, registryReplacement := range registryDest {
				replacedImage := strings.Replace(image, registrySource, registryReplacement, 1)

				// Attempt to lookup image with mirror registry destination
				releaseImage, err := p.Delegate.Lookup(ctx, replacedImage, pullSecret)
				if releaseImage != nil {
					// Verify mirror image availability.
					if _, _, err = repoSetup(ctx, replacedImage, pullSecret); err == nil {
						p.mirroredReleaseImage = replacedImage
						mirrorLookupTotal.WithLabelValues(mirrorResultHit).Inc()
						return releaseImage, nil
					}
					logger.Info("WARNING: The current mirror image is unavailable, continue scanning multiple mirrors", "error", err.Error(), "mirror image", image)
					mirrorLookupTotal.WithLabelValues(mirrorResultUnavailable).Inc()
					continue
				}

				logger.Error(err, "Failed to look up release image using registry mirror", "registry mirror", registryReplacement)
				mirrorLookupTotal.WithLabelValues(mirrorResultError).Inc()
			}
		}
	}

	// Reset mirrored release image when falling back to original
	p.mirroredReleaseImage = ""
	mirrorLookupTotal.WithLabelValues(mirrorResultFallback).Inc()
	return p.Delegate.Lookup(ctx, image, pullSecret)
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetRegistryOverrides() map[string]string {
	return p.Delegate.GetRegistryOverrides()
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetOpenShiftImageRegistryOverrides() map[string][]string {
	return p.OpenShiftImageRegistryOverrides
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) GetMirroredReleaseImage() string {
	return p.mirroredReleaseImage
}
