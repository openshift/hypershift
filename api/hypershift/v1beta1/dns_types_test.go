package v1beta1

import (
	"encoding/json"
	"testing"
)

// dnsSpecNMinus1 represents the previous version of DNSSpec, before the
// ingressAcmeChallengeTarget field was added. It verifies that JSON produced
// by the current type deserializes cleanly into the previous shape (the new
// optional field is simply ignored) and that JSON produced by the previous
// shape deserializes into the current type with the new field left empty.
type dnsSpecNMinus1 struct {
	BaseDomain       string  `json:"baseDomain"`                 //nolint:kubeapilinter // test-only N-1 compat struct
	BaseDomainPrefix *string `json:"baseDomainPrefix,omitempty"` //nolint:kubeapilinter // test-only N-1 compat struct
	PublicZoneID     string  `json:"publicZoneID,omitempty"`     //nolint:kubeapilinter // test-only N-1 compat struct
	PrivateZoneID    string  `json:"privateZoneID,omitempty"`    //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestDNSSpecSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       DNSSpec
		expectedJSON  string
		nMinus1Result dnsSpecNMinus1
	}{
		{
			name: "When ingressAcmeChallengeTarget is set it should serialize and be ignored by N-1",
			current: DNSSpec{
				BaseDomain:                 "example.com",
				IngressAcmeChallengeTarget: "_acme-challenge.apps.central.example.com",
			},
			expectedJSON:  `{"baseDomain":"example.com","ingressAcmeChallengeTarget":"_acme-challenge.apps.central.example.com"}`,
			nMinus1Result: dnsSpecNMinus1{BaseDomain: "example.com"},
		},
		{
			name: "When ingressAcmeChallengeTarget is empty it should be omitted from JSON",
			current: DNSSpec{
				BaseDomain: "example.com",
			},
			expectedJSON:  `{"baseDomain":"example.com"}`,
			nMinus1Result: dnsSpecNMinus1{BaseDomain: "example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 dnsSpecNMinus1
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
			var back DNSSpec
			if err := json.Unmarshal(nMinus1JSON, &back); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if back.IngressAcmeChallengeTarget != "" {
				t.Errorf("expected ingressAcmeChallengeTarget to be empty after N-1 round-trip, got %q", back.IngressAcmeChallengeTarget)
			}
			if back.BaseDomain != tt.current.BaseDomain {
				t.Errorf("unexpected BaseDomain after round-trip: got %q, want %q", back.BaseDomain, tt.current.BaseDomain)
			}
		})
	}
}
