package olm

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/api/image/docker10"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
)

func TestGetCatalogToImageWithISImageRegistryOverrides(t *testing.T) {
	tests := []struct {
		name                     string
		catalogToImage           map[string]string
		isImageRegistryOverrides map[string][]string
		expected                 map[string]string
	}{
		{
			name: "No overrides",
			catalogToImage: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.16",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.16",
			},
			isImageRegistryOverrides: map[string][]string{},
			expected: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.16",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.16",
			},
		},
		{
			name: "Single override and different tag",
			catalogToImage: map[string]string{
				"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.17",
				"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.17",
			},
			isImageRegistryOverrides: map[string][]string{
				"registry.redhat.io": {"custom.registry.io"},
			},
			expected: map[string]string{
				"certified-operators": "custom.registry.io/redhat/certified-operator-index:v4.17",
				"community-operators": "custom.registry.io/redhat/community-operator-index:v4.17",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := getCatalogToImageWithISImageRegistryOverrides(tt.catalogToImage, tt.isImageRegistryOverrides)
			g.Expect(result).To(Equal(tt.expected), "Expected %d entries, but got %d", len(tt.expected), len(result))
			for key, expectedValue := range tt.expected {
				g.Expect(expectedValue).To(Equal(result[key]), "For key %s, expected %s, but got %s", key, expectedValue, result[key])
			}
		})
	}
}

func TestGetCatalogImages(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	pullSecret := []byte("12345")
	fakeMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result: &dockerv1client.DockerImageConfig{
			Config: &docker10.DockerConfig{Labels: map[string]string{"io.openshift.release": "4.18.0"}},
		},
		Manifest: fakeimagemetadataprovider.FakeManifest{},
	}

	// Test that GetCatalogImages returns default operator list if "guest" cluster
	catalogImageOutput, err := GetCatalogImages(ctx, hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{OLMCatalogPlacement: "guest"}}, pullSecret, fakeMetadataProvider, make(map[string][]string))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(catalogImageOutput).To(Equal(map[string]string{
		"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.18",
		"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.18",
		"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.18",
		"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.18",
	}))

	// Test that GetCatalogImages returns an error when "management" cluster is not able to verify catalog images
	catalogImageOutput, err = GetCatalogImages(ctx, hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{OLMCatalogPlacement: "management"}}, pullSecret, fakeMetadataProvider, make(map[string][]string))
	g.Expect(err).To(HaveOccurred())
	g.Expect(catalogImageOutput).To(BeNil())

}
