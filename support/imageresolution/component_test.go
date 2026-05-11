package imageresolution

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestComponentProvider_GetImage(t *testing.T) {
	release := &ReleaseImage{
		ComponentImages: map[string]string{
			"kube-apiserver": "mirror.io/kube-apiserver@sha256:abc",
			"etcd":           "mirror.io/etcd@sha256:def",
		},
		ComponentVersions: map[string]string{
			"release":        "4.17.0",
			"kube-apiserver": "1.30.0",
		},
	}
	provider := newComponentProvider(release)

	t.Run("When component exists, it should return resolved image", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(provider.GetImage("kube-apiserver")).To(Equal("mirror.io/kube-apiserver@sha256:abc"))
	})

	t.Run("When component is missing, it should return empty and track", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(provider.GetImage("nonexistent")).To(BeEmpty())
		g.Expect(provider.missingImages).To(ContainElement("nonexistent"))
	})

	t.Run("When checking image existence, it should return correct bool", func(t *testing.T) {
		g := NewWithT(t)
		img, ok := provider.ImageExist("etcd")
		g.Expect(ok).To(BeTrue())
		g.Expect(img).To(Equal("mirror.io/etcd@sha256:def"))

		_, ok = provider.ImageExist("nonexistent2")
		g.Expect(ok).To(BeFalse())
	})

	t.Run("When getting version, it should return release version", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(provider.Version()).To(Equal("4.17.0"))
	})

	t.Run("When getting component versions, it should return all", func(t *testing.T) {
		g := NewWithT(t)
		versions := provider.ComponentVersions()
		g.Expect(versions).To(HaveKeyWithValue("kube-apiserver", "1.30.0"))
	})

	t.Run("When getting component images, it should return full map", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(provider.ComponentImages()).To(HaveLen(2))
	})
}
