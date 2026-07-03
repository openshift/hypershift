package controlplanecomponent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeReader is a minimal client.Reader for testing GVKAccessCache.
type fakeReader struct {
	err       error
	callCount atomic.Int32
}

func (f *fakeReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	f.callCount.Add(1)
	return f.err
}

func (f *fakeReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func newObj(gvk schema.GroupVersionKind) client.Object {
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
	}
	obj.SetGroupVersionKind(gvk)
	return obj
}

var testGVK = schema.GroupVersionKind{Group: "secrets-store.csi.x-k8s.io", Version: "v1", Kind: "SecretProviderClass"}

func TestGVKAccessCache(t *testing.T) {
	t.Run("When uncached reader returns Forbidden it should skip and cache", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: apierrors.NewForbidden(schema.GroupResource{Group: testGVK.Group, Resource: "secretproviderclasses"}, "test", fmt.Errorf("forbidden"))}
		cache := NewGVKAccessCache(reader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeFalse())

		// Second call should not hit the reader.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeFalse())
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When uncached reader returns NoMatch it should skip and cache", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: testGVK.Group, Kind: testGVK.Kind}}}
		cache := NewGVKAccessCache(reader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeFalse())

		// Second call should not hit the reader.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeFalse())
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When uncached reader returns NotFound it should mark accessible", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: apierrors.NewNotFound(schema.GroupResource{Group: testGVK.Group, Resource: "secretproviderclasses"}, "test")}
		cache := NewGVKAccessCache(reader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())

		// Second call should not hit the reader.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When uncached reader returns OK it should mark accessible", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: nil}
		cache := NewGVKAccessCache(reader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())

		// Second call should not hit the reader.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())
		g.Expect(reader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When uncached reader returns other error it should not cache", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: fmt.Errorf("network timeout")}
		cache := NewGVKAccessCache(reader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).To(HaveOccurred())
		g.Expect(accessible).To(BeFalse())

		// Second call should hit the reader again since result was not cached.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).To(HaveOccurred())
		g.Expect(accessible).To(BeFalse())
		g.Expect(reader.callCount.Load()).To(Equal(int32(2)))
	})

	t.Run("When cache is populated it should not call uncached reader", func(t *testing.T) {
		g := NewWithT(t)
		// First call with a successful reader to populate the cache.
		successReader := &fakeReader{err: nil}
		cache := NewGVKAccessCache(successReader)

		accessible, err := cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())
		g.Expect(successReader.callCount.Load()).To(Equal(int32(1)))

		// Second call should use the cache and not call the reader again.
		accessible, err = cache.GetOrProbe(t.Context(), newObj(testGVK))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())
		g.Expect(successReader.callCount.Load()).To(Equal(int32(1)))
	})

	t.Run("When GVK is empty it should return accessible without probing", func(t *testing.T) {
		g := NewWithT(t)
		reader := &fakeReader{err: fmt.Errorf("should not be called")}
		cache := NewGVKAccessCache(reader)

		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test-ns",
			},
		}
		// Don't set GVK - it will be empty.

		accessible, err := cache.GetOrProbe(t.Context(), obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(accessible).To(BeTrue())
		g.Expect(reader.callCount.Load()).To(Equal(int32(0)))
	})
}
