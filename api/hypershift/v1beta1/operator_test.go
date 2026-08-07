package v1beta1

import (
	"encoding/json"
	"testing"
)

// ingressOperatorSpecNMinus1 represents the previous version of IngressOperatorSpec
// without the DefaultCertificate field.
type ingressOperatorSpecNMinus1 struct {
	EndpointPublishingStrategy json.RawMessage `json:"endpointPublishingStrategy,omitempty"` //nolint:kubeapilinter
}

func TestIngressOperatorSpecSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       IngressOperatorSpec
		expectedJSON  string
		nMinus1Result ingressOperatorSpecNMinus1
	}{
		{
			name:          "When DefaultCertificate is zero it should be omitted and N-1 should deserialize cleanly",
			current:       IngressOperatorSpec{},
			expectedJSON:  `{}`,
			nMinus1Result: ingressOperatorSpecNMinus1{},
		},
		{
			name: "When DefaultCertificate is set it should serialize and N-1 should ignore it",
			current: IngressOperatorSpec{
				DefaultCertificate: IngressDefaultCertificateReference{
					Name: "my-cert",
				},
			},
			expectedJSON:  `{"defaultCertificate":{"name":"my-cert"}}`,
			nMinus1Result: ingressOperatorSpecNMinus1{},
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

			// N -> N-1: old code should ignore the unknown DefaultCertificate field
			var nMinus1 ingressOperatorSpecNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}

			// N-1 -> N: data from old code should deserialize into new struct with zero DefaultCertificate
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTrip IngressOperatorSpec
			if err := json.Unmarshal(nMinus1Data, &roundTrip); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTrip.DefaultCertificate.Name != "" {
				t.Errorf("expected DefaultCertificate to be zero after N-1 round-trip, got %+v", roundTrip.DefaultCertificate)
			}
		})
	}
}
