package imageprovider

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewFromImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		images map[string]string
	}{
		{
			name: "When NewFromImages is called with a map, it should create a provider with those images",
			images: map[string]string{
				"component-a": "registry.example.com/component-a:latest",
				"component-b": "registry.example.com/component-b:v1.0",
			},
		},
		{
			name:   "When NewFromImages is called with an empty map, it should create a provider with no images",
			images: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			provider := NewFromImages(tt.images)

			g.Expect(provider).ToNot(BeNil())
			g.Expect(provider.ComponentImages()).To(Equal(tt.images))
			g.Expect(provider.GetMissingImages()).To(BeEmpty())
		})
	}
}

func TestGetImage(t *testing.T) {
	t.Parallel()

	t.Run("When GetImage is called with an existing key, it should return the image", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"etcd": "registry.example.com/etcd:latest",
		})

		image := provider.GetImage("etcd")

		g.Expect(image).To(Equal("registry.example.com/etcd:latest"))
		g.Expect(provider.GetMissingImages()).To(BeEmpty())
	})

	t.Run("When GetImage is called with a missing key, it should return empty string and track the missing image", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		image := provider.GetImage("nonexistent")

		g.Expect(image).To(BeEmpty())
		g.Expect(provider.GetMissingImages()).To(ConsistOf("nonexistent"))
	})

	t.Run("When GetImage is called with an empty string value, it should track the key as missing", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"empty-image": "",
		})

		image := provider.GetImage("empty-image")

		g.Expect(image).To(BeEmpty())
		g.Expect(provider.GetMissingImages()).To(ConsistOf("empty-image"))
	})

	t.Run("When GetImage is called multiple times with missing keys, it should track all missing images", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"exists": "registry.example.com/exists:latest",
		})

		provider.GetImage("missing-a")
		provider.GetImage("missing-b")
		provider.GetImage("exists")

		g.Expect(provider.GetMissingImages()).To(ConsistOf("missing-a", "missing-b"))
	})
}

func TestImageExist(t *testing.T) {
	t.Parallel()

	t.Run("When ImageExist is called with an existing key, it should return the image and true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"kube-apiserver": "registry.example.com/kube-apiserver:v1.30",
		})

		image, exists := provider.ImageExist("kube-apiserver")

		g.Expect(exists).To(BeTrue())
		g.Expect(image).To(Equal("registry.example.com/kube-apiserver:v1.30"))
	})

	t.Run("When ImageExist is called with a missing key, it should return empty string and false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		image, exists := provider.ImageExist("nonexistent")

		g.Expect(exists).To(BeFalse())
		g.Expect(image).To(BeEmpty())
	})
}

func TestGetMissingImages(t *testing.T) {
	t.Parallel()

	t.Run("When no missing images exist, it should return an empty slice", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{
			"component": "registry.example.com/component:latest",
		})

		g.Expect(provider.GetMissingImages()).To(BeEmpty())
	})

	t.Run("When GetImage has been called with missing keys, it should return those keys", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		provider := NewFromImages(map[string]string{})

		provider.GetImage("alpha")
		provider.GetImage("beta")

		g.Expect(provider.GetMissingImages()).To(ConsistOf("alpha", "beta"))
	})
}

func TestComponentImages(t *testing.T) {
	t.Parallel()

	t.Run("When ComponentImages is called, it should return the images map", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		images := map[string]string{
			"component-a": "registry.example.com/component-a:latest",
			"component-b": "registry.example.com/component-b:v2.0",
		}
		provider := NewFromImages(images)

		result := provider.ComponentImages()

		g.Expect(result).To(Equal(images))
	})
}
