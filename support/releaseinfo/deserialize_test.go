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
		if _, err := DeserializeImageMetadata(imageMetadata); err != nil {
			t.Fatal(err)
		}
	}
}
