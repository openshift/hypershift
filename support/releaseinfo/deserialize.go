package releaseinfo

import (
	"bytes"
	"encoding/json"
	"fmt"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func DeserializeImageStream(data []byte) (*imageapi.ImageStream, error) {
	var imageStream imageapi.ImageStream
	if err := json.Unmarshal(data, &imageStream); err != nil {
		return nil, fmt.Errorf("couldn't read image stream data as a serialized ImageStream: %w\nraw data:\n%s", err, string(data))
	}
	return &imageStream, nil
}

func DeserializeImageMetadata(data []byte) (*CoreOSStreamMetadata, error) {
	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 100).Decode(&coreOSMetaCM); err != nil {
		return nil, fmt.Errorf("couldn't read image lookup data as serialized ConfigMap: %w\nraw data:\n%s", err, string(data))
	}
	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if !hasStreamData {
		return nil, fmt.Errorf("coreos stream metadata configmap is missing the 'stream' key")
	}
	var coreOSMeta CoreOSStreamMetadata
	if err := json.Unmarshal([]byte(streamData), &coreOSMeta); err != nil {
		return nil, fmt.Errorf("couldn't decode stream metadata data: %w\n%s", err, streamData)
	}
	return &coreOSMeta, nil
}
