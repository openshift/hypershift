package releaseinfo

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestDeserializeImageStream(t *testing.T) {
	for _, imageStream := range [][]byte{fixtures.ImageReferencesJSON_4_8, fixtures.ImageReferencesJSON_4_10} {
		if _, err := DeserializeImageStream(imageStream); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeserializeImageMetadata(t *testing.T) {
	for _, imageMetadata := range [][]byte{fixtures.CoreOSBootImagesYAML_4_8, fixtures.CoreOSBootImagesYAML_4_10} {
		var coreOSMetadata *stream.Stream
		coreOSMetadata, err := DeserializeImageMetadata(imageMetadata)
		if err != nil {
			t.Fatal(err)
		}

		arch, ok := coreOSMetadata.Architectures["x86_64"]
		if !ok {
			t.Fatal("missing x86_64 architecture")
		}

		if arch.RHELCoreOSExtensions == nil || arch.RHELCoreOSExtensions.AzureDisk == nil || arch.RHELCoreOSExtensions.AzureDisk.URL == "" {
			t.Fatal("missing azure disk URL")
		}

	}
}

func TestDeserializeMultiStreamImageMetadata(t *testing.T) {
	t.Run("When parsing a single-stream ConfigMap it should return the stream as default with nil Streams", func(t *testing.T) {
		g := NewGomegaWithT(t)

		result, err := DeserializeMultiStreamImageMetadata(fixtures.CoreOSBootImagesYAML_4_10)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.Default).NotTo(BeNil())
		g.Expect(result.Default.Stream).To(Equal("rhcos-4.10"))
		g.Expect(result.Streams).To(BeNil())
		g.Expect(result.DefaultName).To(BeEmpty())
	})

	t.Run("When parsing a multi-stream ConfigMap it should return all streams and identify the default", func(t *testing.T) {
		g := NewGomegaWithT(t)

		result, err := DeserializeMultiStreamImageMetadata(fixtures.CoreOSBootImagesYAML_5_0)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.Default).NotTo(BeNil())
		g.Expect(result.Default.Stream).To(Equal("scos-5.0"))
		g.Expect(result.Streams).To(HaveLen(2))
		g.Expect(result.Streams).To(HaveKey("rhel-9"))
		g.Expect(result.Streams).To(HaveKey("rhel-10"))
		g.Expect(result.DefaultName).To(Equal("rhel-9"))

		// Verify distinct image data between streams
		rhel9Arch := result.Streams["rhel-9"].Architectures["x86_64"]
		rhel10Arch := result.Streams["rhel-10"].Architectures["x86_64"]
		g.Expect(rhel9Arch.Images.Aws.Regions["us-east-1"].Image).To(Equal("ami-rhel9-x86-useast1"))
		g.Expect(rhel10Arch.Images.Aws.Regions["us-east-1"].Image).To(Equal("ami-rhel10-x86-useast1"))
		g.Expect(rhel9Arch.Images.Gcp.Name).To(Equal("rhel9-gcp-image"))
		g.Expect(rhel10Arch.Images.Gcp.Name).To(Equal("rhel10-gcp-image"))
	})

	t.Run("When DeserializeImageMetadata is called on a multi-stream ConfigMap it should return default stream only", func(t *testing.T) {
		g := NewGomegaWithT(t)

		defaultStream, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_5_0)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(defaultStream).NotTo(BeNil())
		g.Expect(defaultStream.Stream).To(Equal("scos-5.0"))

		// Verify it returns the same data as the multi-stream default
		result, err := DeserializeMultiStreamImageMetadata(fixtures.CoreOSBootImagesYAML_5_0)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(defaultStream.Stream).To(Equal(result.Default.Stream))
	})
}
