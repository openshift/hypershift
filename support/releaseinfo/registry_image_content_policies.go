package releaseinfo

import (
	"context"
	"strings"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
)

var _ ProviderWithOpenShiftImageRegistryOverrides = (*ProviderWithOpenShiftImageRegistryOverridesDecorator)(nil)

type ProviderWithOpenShiftImageRegistryOverridesDecorator struct {
	Delegate                        ProviderWithRegistryOverrides
	OpenShiftImageRegistryOverrides map[string][]string
	mirroredReleaseImage            string

	lock sync.Mutex
}

func (p *ProviderWithOpenShiftImageRegistryOverridesDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	logger := ctrl.LoggerFrom(ctx)

	for registrySource, registryDest := range p.OpenShiftImageRegistryOverrides {
		if strings.Contains(image, registrySource) {
			for _, registryReplacement := range registryDest {
				image = strings.Replace(image, registrySource, registryReplacement, 1)

				// Attempt to lookup image with mirror registry destination
				releaseImage, err := p.Delegate.Lookup(ctx, image, pullSecret)
				if releaseImage != nil {
					p.mirroredReleaseImage = image
					return releaseImage, nil
				}

				logger.Error(err, "Failed to look up release image using registry mirror", "registry mirror", registryReplacement)
			}
		}
	}

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
