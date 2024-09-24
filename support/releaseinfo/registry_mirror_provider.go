package releaseinfo

import (
	"context"
	"strings"
	"sync"
)

var _ ProviderWithRegistryOverrides = (*RegistryMirrorProviderDecorator)(nil)

// RegistryMirrorProviderDecorator decorates another Provider to add user-specified
// component name to image mappings. The Lookup implementation will first
// delegate to the given Delegate, and will then add additional TagReferences
// to the Delegate's results based on the ComponentImages.
type RegistryMirrorProviderDecorator struct {
	Delegate Provider
	// RegistryOverrides contains the source registry string as a key and the destination registry string as value.
	// images before being applied are scanned for the source registry string and if found the string is replaced with
	// the destination registry string. This allows hypershift to run in non-crio environments where mirroring is not
	// applicable.
	RegistryOverrides map[string]string

	lock sync.Mutex
}

func (p *RegistryMirrorProviderDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	releaseImage, err := p.Delegate.Lookup(ctx, image, pullSecret)
	if err != nil {
		return nil, err
	}

	imageStream := releaseImage.ImageStream.DeepCopy() // deepCopy so the cache is not overridden.
	for i := range imageStream.Spec.Tags {
		for registrySource, registryDest := range p.RegistryOverrides {
			imageStream.Spec.Tags[i].From.Name = strings.Replace(imageStream.Spec.Tags[i].From.Name, registrySource, registryDest, 1)
		}
	}

	return &ReleaseImage{
		ImageStream:    imageStream,
		StreamMetadata: releaseImage.StreamMetadata,
	}, nil
}

func (p *RegistryMirrorProviderDecorator) GetRegistryOverrides() map[string]string {
	result := map[string]string{}
	for k, v := range p.RegistryOverrides {
		result[k] = v
	}
	return result
}
