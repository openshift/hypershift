package releaseinfo

import (
	"testing"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
)

func TestDeserializeImageStream(t *testing.T) {
	if _, err := DeserializeImageStream(fixtures.ImageReferencesJSON_4_8); err != nil {
		t.Fatal(err)
	}
}

func TestDeserializeImageMetadata(t *testing.T) {
	if _, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_4_8); err != nil {
		t.Fatal(err)
	}
}
