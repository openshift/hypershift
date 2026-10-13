package releaseinfo

import (
	"testing"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
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
		var coreOSMetadata *CoreOSStreamMetadata
		coreOSMetadata, err := DeserializeImageMetadata(imageMetadata)
		if err != nil {
			t.Fatal(err)
		}

		if _, ok := coreOSMetadata.Architectures["x86_64"]; !ok {
			t.Fatal(err)
		}

		if coreOSMetadata.Architectures["x86_64"].RHCOS.AzureDisk.URL == "" {
			t.Fatal(err)
		}

	}
}
