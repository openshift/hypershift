package releaseinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

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

// DeserializeImageMetadata parses a CoreOS boot images ConfigMap and returns the
// default stream metadata. For callers that need multi-stream support, use
// DeserializeMultiStreamImageMetadata instead.
func DeserializeImageMetadata(data []byte) (*stream.Stream, error) {
	result, err := DeserializeMultiStreamImageMetadata(data)
	if err != nil {
		return nil, err
	}
	return result.Default, nil
}

// StreamsResult holds the parsed result of a multi-stream CoreOS boot images
// ConfigMap. Default is the primary stream (from the "stream" key), Streams
// contains all named streams (from the "streams" key), and DefaultName is the
// key in Streams that corresponds to Default.
type StreamsResult struct {
	Default     *stream.Stream
	Streams     map[string]*stream.Stream
	DefaultName string
}

// DeserializeMultiStreamImageMetadata parses a CoreOS boot images ConfigMap that
// may contain both the legacy single "stream" key and the new "streams" key
// (a JSON map keyed by stream name such as "rhel-9" or "rhel-10"). It returns
// all parsed streams along with the identified default.
func DeserializeMultiStreamImageMetadata(data []byte) (*StreamsResult, error) {
	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 100).Decode(&coreOSMetaCM); err != nil {
		return nil, fmt.Errorf("couldn't read image lookup data as serialized ConfigMap: %w\nraw data:\n%s", err, string(data))
	}

	var defaultStream *stream.Stream
	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if hasStreamData {
		var s stream.Stream
		if err := json.Unmarshal([]byte(streamData), &s); err != nil {
			return nil, fmt.Errorf("couldn't decode stream metadata data: %w\n%s", err, streamData)
		}
		defaultStream = &s
	}

	var streams map[string]*stream.Stream
	streamsData, hasStreamsData := coreOSMetaCM.Data["streams"]
	if hasStreamsData {
		if err := json.Unmarshal([]byte(streamsData), &streams); err != nil {
			return nil, fmt.Errorf("couldn't decode multi-stream metadata data: %w\n%s", err, streamsData)
		}
		if len(streams) == 0 {
			return nil, fmt.Errorf("'streams' key is present but empty")
		}
		for name, s := range streams {
			if s == nil {
				return nil, fmt.Errorf("stream %q has null metadata", name)
			}
		}
	}

	if defaultStream == nil && streams == nil {
		return nil, fmt.Errorf("coreos stream metadata configmap is missing both 'stream' and 'streams' keys")
	}

	// If only the legacy "stream" key is present, return it as the default
	// with no multi-stream map.
	if streams == nil {
		return &StreamsResult{
			Default: defaultStream,
		}, nil
	}

	// Identify which entry in streams matches the default stream.
	defaultName := matchDefaultStreamName(defaultStream, streams)

	// If no "stream" key was present, pick the first alphabetical stream as default.
	if defaultStream == nil {
		defaultName = firstAlphabeticalKey(streams)
		defaultStream = streams[defaultName]
	}

	return &StreamsResult{
		Default:     defaultStream,
		Streams:     streams,
		DefaultName: defaultName,
	}, nil
}

// matchDefaultStreamName finds the key in the streams map whose Stream field
// matches the default stream's Stream field. If no match is found, it falls
// back to the first alphabetical key.
func matchDefaultStreamName(defaultStream *stream.Stream, streams map[string]*stream.Stream) string {
	if defaultStream == nil {
		return firstAlphabeticalKey(streams)
	}
	for name, s := range streams {
		if s.Stream == defaultStream.Stream {
			return name
		}
	}
	return firstAlphabeticalKey(streams)
}

// firstAlphabeticalKey returns the first key from a map in alphabetical order.
func firstAlphabeticalKey(m map[string]*stream.Stream) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}
