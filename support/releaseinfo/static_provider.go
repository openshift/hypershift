package releaseinfo

import (
	"context"
	"sync"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
)

var _ Provider = (*StaticProviderDecorator)(nil)

// StaticProviderDecorator decorates another Provider to add user-specified
// component name to image mappings. The Lookup implementation will first
// delegate to the given Delegate, and will then add additional TagReferences
// to the Delegate's results based on the ComponentImages.
type StaticProviderDecorator struct {
	Delegate        Provider
	ComponentImages map[string]string

	lock sync.Mutex
}

func (p *StaticProviderDecorator) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	releaseImage, err := p.Delegate.Lookup(ctx, image, pullSecret)
	if err != nil {
		return nil, err
	}
	if p.ComponentImages == nil {
		return releaseImage, nil
	}
	for component, image := range p.ComponentImages {
		ref := imageapi.TagReference{
			Name: component,
			From: &corev1.ObjectReference{
				Name: image,
			},
		}
		releaseImage.Spec.Tags = append(releaseImage.Spec.Tags, ref) //TODO(cewong): ensure we're not adding tags that are already in the map!
	}
	return releaseImage, nil
}
