package releaseinfo

import (
	"bytes"
	"encoding/json"
	"fmt"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/coreos/stream-metadata-go/stream"
)

func DeserializeImageStream(data []byte) (*imageapi.ImageStream, error) {
	var imageStream imageapi.ImageStream
	if err := json.Unmarshal(data, &imageStream); err != nil {
		return nil, fmt.Errorf("couldn't read image stream data as a serialized ImageStream: %w\nraw data:\n%s", err, string(data))
	}
	return &imageStream, nil
}

// ImageMetadataResult contains parsed CoreOS stream metadata from a release payload.
type ImageMetadataResult struct {
	// Stream is the legacy single-stream metadata (always populated).
	Stream *stream.Stream
	// Streams is multi-stream metadata keyed by stream name (nil for older payloads).
	Streams map[string]*stream.Stream
}

// DeserializeImageMetadataMultiStream parses a CoreOS stream metadata ConfigMap,
// supporting both the legacy single "stream" key and the newer "streams" key
// containing multiple stream entries.
func DeserializeImageMetadataMultiStream(data []byte) (*ImageMetadataResult, error) {
	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 100).Decode(&coreOSMetaCM); err != nil {
		return nil, fmt.Errorf("couldn't read image lookup data as serialized ConfigMap: %w\nraw data:\n%s", err, string(data))
	}

	result := &ImageMetadataResult{}

	// Parse the legacy "stream" key (required for all payloads).
	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if !hasStreamData {
		return nil, fmt.Errorf("coreos stream metadata configmap is missing the 'stream' key")
	}
	var coreOSMeta stream.Stream
	if err := json.Unmarshal([]byte(streamData), &coreOSMeta); err != nil {
		return nil, fmt.Errorf("couldn't decode stream metadata data: %w\n%s", err, streamData)
	}
	result.Stream = &coreOSMeta

	// Parse the optional "streams" key for multi-stream payloads.
	streamsData, hasStreamsData := coreOSMetaCM.Data["streams"]
	if hasStreamsData {
		var streams map[string]*stream.Stream
		if err := json.Unmarshal([]byte(streamsData), &streams); err != nil {
			return nil, fmt.Errorf("couldn't decode multi-stream metadata data: %w\n%s", err, streamsData)
		}
		result.Streams = streams
	}

	return result, nil
}

func DeserializeImageMetadata(data []byte) (*stream.Stream, error) {
	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 100).Decode(&coreOSMetaCM); err != nil {
		return nil, fmt.Errorf("couldn't read image lookup data as serialized ConfigMap: %w\nraw data:\n%s", err, string(data))
	}
	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if !hasStreamData {
		return nil, fmt.Errorf("coreos stream metadata configmap is missing the 'stream' key")
	}
	var coreOSMeta stream.Stream
	if err := json.Unmarshal([]byte(streamData), &coreOSMeta); err != nil {
		return nil, fmt.Errorf("couldn't decode stream metadata data: %w\n%s", err, streamData)
	}
	return &coreOSMeta, nil
}
