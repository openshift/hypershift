package v1beta1

import (
	"encoding/json"
	"testing"
)

// gcpPlatformSpecNMinus1 represents the previous version of GCPPlatformSpec,
// before the delegation field was added. It verifies that JSON produced by the
// current type deserializes cleanly into the previous shape (the new optional
// field is simply ignored) and that JSON produced by the previous shape
// deserializes into the current type with the new field left empty.
type gcpPlatformSpecNMinus1 struct {
	Project string `json:"project"` //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestGCPPlatformSpecSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       GCPPlatformSpec
		expectedJSON  string
		nMinus1Result gcpPlatformSpecNMinus1
	}{
		{
			name: "When delegation is set it should serialize and be ignored by N-1",
			current: GCPPlatformSpec{
				Project: "my-project-123",
				Delegation: GCPDNSDelegationSpec{
					ACMEChallengeTarget: "_acme-challenge.apps.central.example.com",
				},
			},
			expectedJSON:  `{"project":"my-project-123","delegation":{"acmeChallengeTarget":"_acme-challenge.apps.central.example.com"}}`,
			nMinus1Result: gcpPlatformSpecNMinus1{Project: "my-project-123"},
		},
		{
			name: "When delegation is empty it should be omitted from JSON",
			current: GCPPlatformSpec{
				Project: "my-project-123",
			},
			expectedJSON:  `{"project":"my-project-123"}`,
			nMinus1Result: gcpPlatformSpecNMinus1{Project: "my-project-123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", data, tt.expectedJSON)
			}

			var nMinus1 gcpPlatformSpecNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1 != tt.nMinus1Result {
				t.Errorf("unexpected N-1 result: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			// Round-trip: N-1 JSON must deserialize into the current type
			// with the new field left empty.
			nMinus1JSON, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var back GCPPlatformSpec
			if err := json.Unmarshal(nMinus1JSON, &back); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if back.Delegation.ACMEChallengeTarget != "" {
				t.Errorf("expected delegation.acmeChallengeTarget to be empty after N-1 round-trip, got %q", back.Delegation.ACMEChallengeTarget)
			}
			if back.Project != tt.current.Project {
				t.Errorf("unexpected Project after round-trip: got %q, want %q", back.Project, tt.current.Project)
			}
		})
	}
}
