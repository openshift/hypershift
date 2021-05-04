package releaseinfo

import (
	"bytes"
	"encoding/json"
	"testing"

	imageapi "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/openshift/hypershift/control-plane-operator/releaseinfo/fixtures"
)

func TestDeserialization(t *testing.T) {
	var imageStream imageapi.ImageStream
	if err := json.Unmarshal(fixtures.ImageReferencesJSON_4_8, &imageStream); err != nil {
		t.Fatal(err)
	}

	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(fixtures.CoreOSBootImagesYAML_4_8), 100).Decode(&coreOSMetaCM); err != nil {
		t.Fatal(err)
	}

	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if !hasStreamData {
		t.Fatal("missing stream key")
	}
	var coreOSMeta CoreOSStreamMetadata
	if err := json.Unmarshal([]byte(streamData), &coreOSMeta); err != nil {
		t.Fatal(err)
	}
}
