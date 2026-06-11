package releaseinfo

import (
	"encoding/json"
	"fmt"
	"testing"

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

func TestDeserializeImageMetadataMultiStream(t *testing.T) {
	testStreamJSON := func(name string) string {
		s := &stream.Stream{
			Stream: name,
			Architectures: map[string]stream.Arch{
				"x86_64": {},
			},
		}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("failed to marshal test stream %q: %v", name, err)
		}
		return string(data)
	}

	makeConfigMapYAML := func(data map[string]string) []byte {
		cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: coreos-bootimages\ndata:\n"
		for k, v := range data {
			cm += fmt.Sprintf("  %s: |\n", k)
			for _, line := range splitLines(v) {
				cm += fmt.Sprintf("    %s\n", line)
			}
		}
		return []byte(cm)
	}

	t.Run("When ConfigMap has only stream key it should parse legacy format and set Streams to nil", func(t *testing.T) {
		streamJSON := testStreamJSON("rhcos-4.10")
		cmData := makeConfigMapYAML(map[string]string{
			"stream": streamJSON,
		})
		result, err := DeserializeImageMetadataMultiStream(cmData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Stream == nil {
			t.Fatal("expected Stream to be populated")
		}
		if result.Stream.Stream != "rhcos-4.10" {
			t.Fatalf("expected Stream.Stream to be %q, got %q", "rhcos-4.10", result.Stream.Stream)
		}
		if _, ok := result.Stream.Architectures["x86_64"]; !ok {
			t.Fatal("expected x86_64 architecture in Stream")
		}
		if result.Streams != nil {
			t.Fatalf("expected Streams to be nil for legacy format, got %v", result.Streams)
		}
	})

	t.Run("When ConfigMap has both stream and streams keys it should parse both", func(t *testing.T) {
		streamJSON := testStreamJSON("rhcos-4.18")
		rhel9StreamJSON := testStreamJSON("rhel-9")
		rhel10StreamJSON := testStreamJSON("rhel-10")
		streamsMapJSON, err := json.Marshal(map[string]*stream.Stream{
			"rhel-9": {
				Stream: "rhel-9",
				Architectures: map[string]stream.Arch{
					"x86_64": {},
				},
			},
			"rhel-10": {
				Stream: "rhel-10",
				Architectures: map[string]stream.Arch{
					"x86_64":  {},
					"aarch64": {},
				},
			},
		})
		if err != nil {
			t.Fatalf("failed to marshal streams map: %v", err)
		}
		// Silence unused variable warnings for individually-constructed streams
		_ = rhel9StreamJSON
		_ = rhel10StreamJSON

		cmData := makeConfigMapYAML(map[string]string{
			"stream":  streamJSON,
			"streams": string(streamsMapJSON),
		})
		result, err := DeserializeImageMetadataMultiStream(cmData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify legacy stream is populated.
		if result.Stream == nil {
			t.Fatal("expected Stream to be populated")
		}
		if result.Stream.Stream != "rhcos-4.18" {
			t.Fatalf("expected Stream.Stream to be %q, got %q", "rhcos-4.18", result.Stream.Stream)
		}

		// Verify multi-stream map is populated.
		if result.Streams == nil {
			t.Fatal("expected Streams to be populated")
		}
		if len(result.Streams) != 2 {
			t.Fatalf("expected 2 entries in Streams, got %d", len(result.Streams))
		}

		rhel9, ok := result.Streams["rhel-9"]
		if !ok {
			t.Fatal("expected rhel-9 entry in Streams")
		}
		if rhel9.Stream != "rhel-9" {
			t.Fatalf("expected rhel-9 stream name, got %q", rhel9.Stream)
		}
		if _, ok := rhel9.Architectures["x86_64"]; !ok {
			t.Fatal("expected x86_64 architecture in rhel-9 stream")
		}

		rhel10, ok := result.Streams["rhel-10"]
		if !ok {
			t.Fatal("expected rhel-10 entry in Streams")
		}
		if rhel10.Stream != "rhel-10" {
			t.Fatalf("expected rhel-10 stream name, got %q", rhel10.Stream)
		}
		if _, ok := rhel10.Architectures["aarch64"]; !ok {
			t.Fatal("expected aarch64 architecture in rhel-10 stream")
		}
	})

	t.Run("When ConfigMap is missing stream key it should return error", func(t *testing.T) {
		cmData := makeConfigMapYAML(map[string]string{
			"releaseVersion": "4.18.0",
		})
		_, err := DeserializeImageMetadataMultiStream(cmData)
		if err == nil {
			t.Fatal("expected error when stream key is missing, got nil")
		}
	})
}

// splitLines splits a string into individual lines for YAML block scalar formatting.
func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
