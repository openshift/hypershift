package releaseinfo

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
)

type fakeProvider struct {
	lookupCount int
}

func (f *fakeProvider) Lookup(_ context.Context, image string, _ []byte) (*ReleaseImage, error) {
	f.lookupCount++
	return &ReleaseImage{ImageStream: nil}, nil
}

func TestCachedProviderCacheHit(t *testing.T) {
	g := NewWithT(t)
	inner := &fakeProvider{}
	provider := &CachedProvider{
		Cache: map[string]*ReleaseImage{},
		Inner: inner,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	image := "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"

	_, err := provider.Lookup(ctx, image, nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(inner.lookupCount).To(Equal(1))

	_, err = provider.Lookup(ctx, image, nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(inner.lookupCount).To(Equal(1))
}

func TestCachedProviderCacheMiss(t *testing.T) {
	g := NewWithT(t)
	inner := &fakeProvider{}
	provider := &CachedProvider{
		Cache: map[string]*ReleaseImage{},
		Inner: inner,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		image := fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:4.%d.0-x86_64", 17+i)
		_, err := provider.Lookup(ctx, image, nil)
		g.Expect(err).ToNot(HaveOccurred())
	}

	g.Expect(inner.lookupCount).To(Equal(3))
}
