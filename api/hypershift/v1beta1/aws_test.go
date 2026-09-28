package v1beta1

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTerminationHandlerQueueURLSerialization verifies that the
// terminationHandlerQueueURL field serializes/deserializes correctly
// with all supported URL formats (standard, FIPS, GovCloud, FIFO).
//
// Note: This PR only changes the validation pattern marker, not the Go type
// (remains string) or JSON wire format. The api/AGENTS.md N-1/N+1 compatibility
// test convention applies to type/shape changes (value→pointer, rename, etc.),
// not validation-marker-only changes. This test serves as a field serialization
// smoke test rather than a cross-version compatibility test.
func TestTerminationHandlerQueueURLSerialization(t *testing.T) {
	tests := []struct {
		name                       string
		terminationHandlerQueueURL string
	}{
		{
			name:                       "standard commercial SQS URL (pre-FIPS pattern)",
			terminationHandlerQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue",
		},
		{
			name:                       "FIPS commercial SQS URL (new pattern)",
			terminationHandlerQueueURL: "https://sqs-fips.us-east-1.amazonaws.com/123456789012/my-queue",
		},
		{
			name:                       "GovCloud SQS FIFO URL (pre-FIPS pattern)",
			terminationHandlerQueueURL: "https://sqs.us-gov-west-1.amazonaws.com/123456789012/my-queue.fifo",
		},
		{
			name:                       "FIPS GovCloud SQS FIFO URL (new pattern)",
			terminationHandlerQueueURL: "https://sqs-fips.us-gov-west-1.amazonaws.com/123456789012/my-queue.fifo",
		},
		{
			name:                       "empty termination handler URL",
			terminationHandlerQueueURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create current (N) version with the URL
			current := AWSPlatformSpec{
				Region:                     "us-east-1",
				RolesRef:                   AWSRolesRef{},
				TerminationHandlerQueueURL: tt.terminationHandlerQueueURL,
			}

			// Marshal current version
			data, err := json.Marshal(current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}

			// Deserialize back into current version (round-trip test)
			var roundTripped AWSPlatformSpec
			if err := json.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("failed to unmarshal JSON back to struct: %v", err)
			}

			if roundTripped.TerminationHandlerQueueURL != tt.terminationHandlerQueueURL {
				t.Errorf("terminationHandlerQueueURL mismatch after round-trip:\ngot:  %s\nwant: %s",
					roundTripped.TerminationHandlerQueueURL, tt.terminationHandlerQueueURL)
			}

			// Verify the Region field also round-tripped correctly
			if roundTripped.Region != current.Region {
				t.Errorf("Region mismatch after round-trip: got %s, want %s",
					roundTripped.Region, current.Region)
			}

			// Verify empty URLs are omitted from JSON (due to omitempty tag)
			if tt.terminationHandlerQueueURL == "" && strings.Contains(string(data), "terminationHandlerQueueURL") {
				t.Errorf("expected terminationHandlerQueueURL to be omitted when empty, but got: %s", string(data))
			}

			// Verify non-empty URLs are included in JSON
			if tt.terminationHandlerQueueURL != "" && !strings.Contains(string(data), tt.terminationHandlerQueueURL) {
				t.Errorf("expected terminationHandlerQueueURL to be in JSON, but got: %s", string(data))
			}
		})
	}
}

// TestTerminationHandlerQueueURLOmittedWhenEmpty verifies that the
// terminationHandlerQueueURL field is omitted from JSON when empty,
// as required by the omitempty tag. This ensures the field doesn't serialize
// as an empty string, which would pass CRD validation but represent unused state.
func TestTerminationHandlerQueueURLOmittedWhenEmpty(t *testing.T) {
	spec := AWSPlatformSpec{
		Region:                     "us-east-1",
		RolesRef:                   AWSRolesRef{},
		TerminationHandlerQueueURL: "",
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// The JSON should not contain terminationHandlerQueueURL when it's empty
	jsonStr := string(data)
	if strings.Contains(jsonStr, "terminationHandlerQueueURL") {
		t.Errorf("expected terminationHandlerQueueURL to be omitted when empty, but got: %s", jsonStr)
	}
}
