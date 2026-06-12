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

func DeserializeImageMetadata(data []byte) (*stream.Stream, map[string]*stream.Stream, error) {
	var coreOSMetaCM corev1.ConfigMap
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 100).Decode(&coreOSMetaCM); err != nil {
		return nil, nil, fmt.Errorf("couldn't read image lookup data as serialized ConfigMap: %w\nraw data:\n%s", err, string(data))
	}

	var osStreams map[string]*stream.Stream
	if streamsData, ok := coreOSMetaCM.Data["streams"]; ok {
		if err := json.Unmarshal([]byte(streamsData), &osStreams); err != nil {
			return nil, nil, fmt.Errorf("couldn't decode multi-stream metadata: %w\n%s", err, streamsData)
		}
	}
	hasOSStreams := len(osStreams) > 0

	streamData, hasStreamData := coreOSMetaCM.Data["stream"]
	if !hasStreamData && !hasOSStreams {
		return nil, nil, fmt.Errorf("coreos stream metadata configmap is missing both 'stream' and 'streams' keys")
	}

	var coreOSMeta *stream.Stream
	if hasStreamData {
		coreOSMeta = &stream.Stream{}
		if err := json.Unmarshal([]byte(streamData), coreOSMeta); err != nil {
			return nil, nil, fmt.Errorf("couldn't decode stream metadata: %w\n%s", err, streamData)
		}
	}

	// No fallback when "stream" is absent: the MCO team confirmed the legacy
	// "stream" key is frozen to rhel-9 and present until rhel-9 EoL (OCP 5.3).
	// Consumers must use StreamForName() to pick the stream they need.
	// Ref: https://redhat-internal.slack.com/archives/C02CZNQHGN8/p1781604462650819

	return coreOSMeta, osStreams, nil
}
