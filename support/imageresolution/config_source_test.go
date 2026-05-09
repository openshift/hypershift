package imageresolution

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
)

func TestStaticConfigSource(t *testing.T) {
	t.Run("When created with config, it should always return the same config", func(t *testing.T) {
		g := NewWithT(t)
		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{"quay.io": "mirror.io"},
		}
		src := newStaticConfigSource(cfg)

		result, err := src.current(context.Background())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(cfg))
	})

	t.Run("When config is empty, it should return empty config", func(t *testing.T) {
		g := NewWithT(t)
		src := newStaticConfigSource(ResolverConfig{})

		result, err := src.current(context.Background())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.IsEmpty()).To(BeTrue())
	})
}

func TestMutableConfigSource(t *testing.T) {
	t.Run("When created, it should return the initial config", func(t *testing.T) {
		g := NewWithT(t)
		cfg := ResolverConfig{
			RegistryOverrides: map[string]string{"quay.io": "mirror.io"},
		}
		src := newMutableConfigSource(cfg)

		result, err := src.current(context.Background())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.RegistryOverrides).To(Equal(map[string]string{"quay.io": "mirror.io"}))
	})

	t.Run("When mirrors are updated, it should return the new mirrors", func(t *testing.T) {
		g := NewWithT(t)
		src := newMutableConfigSource(ResolverConfig{
			RegistryOverrides: map[string]string{"quay.io": "mirror.io"},
		})

		newMirrors := map[string][]string{
			"quay.io/openshift": {"mirror.io/openshift"},
		}
		src.updateMirrors(newMirrors)

		result, err := src.current(context.Background())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.ImageRegistryMirrors).To(Equal(newMirrors))
		g.Expect(result.RegistryOverrides).To(Equal(map[string]string{"quay.io": "mirror.io"}))
	})
}
