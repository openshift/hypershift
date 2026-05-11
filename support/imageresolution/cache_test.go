package imageresolution

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestCloneReleaseImage(t *testing.T) {
	t.Run("When input is nil, it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(cloneReleaseImage(nil)).To(BeNil())
	})
}

func TestReleaseCache(t *testing.T) {
	t.Run("When entry is cached and within TTL, it should return it", func(t *testing.T) {
		g := NewWithT(t)
		c := newReleaseCache(time.Hour)
		release := &ReleaseImage{
			ComponentImages: map[string]string{"etcd": "quay.io/etcd:latest"},
		}
		c.put("quay.io/ocp:4.17", release)
		g.Expect(c.get("quay.io/ocp:4.17")).To(Equal(release))
	})

	t.Run("When entry is expired, it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		c := newReleaseCache(0)
		release := &ReleaseImage{
			ComponentImages: map[string]string{"etcd": "quay.io/etcd:latest"},
		}
		c.put("quay.io/ocp:4.17", release)
		g.Expect(c.get("quay.io/ocp:4.17")).To(BeNil())
	})

	t.Run("When key is not present, it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		c := newReleaseCache(time.Hour)
		g.Expect(c.get("nonexistent")).To(BeNil())
	})
}

func TestMirrorAvailabilityCache(t *testing.T) {
	t.Run("When mirror is cached as available, it should return true", func(t *testing.T) {
		g := NewWithT(t)
		c := newMirrorAvailabilityCache(time.Hour)
		c.set("mirror1.io", true)
		avail, ok := c.get("mirror1.io")
		g.Expect(ok).To(BeTrue())
		g.Expect(avail).To(BeTrue())
	})

	t.Run("When mirror is cached as unavailable, it should return false", func(t *testing.T) {
		g := NewWithT(t)
		c := newMirrorAvailabilityCache(time.Hour)
		c.set("mirror1.io", false)
		avail, ok := c.get("mirror1.io")
		g.Expect(ok).To(BeTrue())
		g.Expect(avail).To(BeFalse())
	})

	t.Run("When mirror cache entry is expired, it should return not-ok", func(t *testing.T) {
		g := NewWithT(t)
		c := newMirrorAvailabilityCache(0)
		c.set("mirror1.io", true)
		_, ok := c.get("mirror1.io")
		g.Expect(ok).To(BeFalse())
	})

	t.Run("When two cache instances exist, they should be independent", func(t *testing.T) {
		g := NewWithT(t)
		c1 := newMirrorAvailabilityCache(time.Hour)
		c2 := newMirrorAvailabilityCache(time.Hour)
		c1.set("mirror1.io", true)
		_, ok := c2.get("mirror1.io")
		g.Expect(ok).To(BeFalse())
	})
}

func TestMetadataCache(t *testing.T) {
	t.Run("When entry is cached and within TTL, it should return it", func(t *testing.T) {
		g := NewWithT(t)
		c := newMetadataCache(time.Hour)
		c.put("key1", "value1")
		val, ok := c.get("key1")
		g.Expect(ok).To(BeTrue())
		g.Expect(val).To(Equal("value1"))
	})

	t.Run("When entry is expired, it should return false and clean up the entry", func(t *testing.T) {
		g := NewWithT(t)
		c := newMetadataCache(time.Millisecond)
		c.put("expiring-key", "some-value")

		// Wait past the TTL so the entry expires.
		time.Sleep(5 * time.Millisecond)

		val, ok := c.get("expiring-key")
		g.Expect(ok).To(BeFalse())
		g.Expect(val).To(BeNil())

		// Verify the entry was deleted from the map.
		c.mu.RLock()
		_, exists := c.entries["expiring-key"]
		c.mu.RUnlock()
		g.Expect(exists).To(BeFalse())
	})

	t.Run("When key is not present, it should return false", func(t *testing.T) {
		g := NewWithT(t)
		c := newMetadataCache(time.Hour)
		val, ok := c.get("nonexistent")
		g.Expect(ok).To(BeFalse())
		g.Expect(val).To(BeNil())
	})
}
