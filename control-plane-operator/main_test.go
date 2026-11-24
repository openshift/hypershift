package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"
)

// registryBackend simulates a registry holding a private release image that only
// the provider credential can read. It counts the lookups that reach it.
type registryBackend struct {
	calls int
}

var providerCredential = []byte(`{"auths":{"quay.io":{"auth":"cHJvdmlkZXI6cHVsbA=="}}}`)

func (b *registryBackend) Lookup(_ context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	b.calls++
	if !bytes.Equal(pullSecret, providerCredential) {
		return nil, errors.New("unauthorized")
	}
	return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{}}, nil
}

// cachedProviderOf returns the CachedProvider at the bottom of a release provider
// built by buildReleaseProviders.
func cachedProviderOf(g Gomega, p *releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator) *releaseinfo.CachedProvider {
	mirror, ok := p.Delegate.(*releaseinfo.RegistryMirrorProviderDecorator)
	g.Expect(ok).To(BeTrue(), "expected a RegistryMirrorProviderDecorator")
	static, ok := mirror.Delegate.(*releaseinfo.StaticProviderDecorator)
	g.Expect(ok).To(BeTrue(), "expected a StaticProviderDecorator")
	cached, ok := static.Delegate.(*releaseinfo.CachedProvider)
	g.Expect(ok).To(BeTrue(), "expected a CachedProvider")
	return cached
}

func TestBuildReleaseProviders(t *testing.T) {
	// A release image cached by a control plane release lookup, which uses the
	// provider credential, must not be returned to a data plane release lookup of
	// the same pull spec. That lookup must reach the registry and be authorized
	// with the cluster pull secret.
	t.Run("When the control plane provider has cached a private release, it should not serve it to a data plane lookup of the same pull spec", func(t *testing.T) {
		g := NewWithT(t)

		cpReleaseProvider, userReleaseProvider := buildReleaseProviders(nil, nil, nil)
		g.Expect(cachedProviderOf(g, cpReleaseProvider)).ToNot(BeIdenticalTo(cachedProviderOf(g, userReleaseProvider)))

		backend := &registryBackend{}
		cachedProviderOf(g, cpReleaseProvider).Inner = backend
		cachedProviderOf(g, userReleaseProvider).Inner = backend

		image := "quay.io/openshift-release-dev/ocp-release-private:4.21.10-x86_64"
		_, err := cpReleaseProvider.Lookup(t.Context(), image, providerCredential)
		g.Expect(err).ToNot(HaveOccurred())

		_, err = userReleaseProvider.Lookup(t.Context(), image, []byte(`{"auths":{}}`))
		g.Expect(err).To(MatchError("unauthorized"))
		g.Expect(backend.calls).To(Equal(2), "expected the data plane lookup to reach the registry")
	})
}
