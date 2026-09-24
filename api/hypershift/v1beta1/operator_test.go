package v1beta1

import (
	"encoding/json"
	"testing"
)

// ingressOperatorSpecNMinus1 represents a reader that does not know the new ingress fields.
type ingressOperatorSpecNMinus1 struct{}

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

func TestOperatorConfigurationSerializationCompatibility(t *testing.T) {
	t.Run("When an older reader ignores ingress configuration it should preserve sibling operator configuration", func(t *testing.T) {
		// Release 4.20 did not have IngressOperatorSpec before this backport.
		type previousOperatorConfiguration struct {
			ClusterNetworkOperator *ClusterNetworkOperatorSpec `json:"clusterNetworkOperator,omitempty"`
		}
		data := []byte(`{"clusterNetworkOperator":{"disableMultiNetwork":true},"ingressOperator":{"defaultCertificate":{"name":"my-cert"}}}`)
		var previous previousOperatorConfiguration
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("older reader failed to deserialize new configuration: %v", err)
		}
		previousData, err := json.Marshal(previous)
		if err != nil {
			t.Fatalf("failed to serialize previous configuration: %v", err)
		}
		var current OperatorConfiguration
		if err := json.Unmarshal(previousData, &current); err != nil {
			t.Fatalf("failed to deserialize previous configuration: %v", err)
		}
		if current.IngressOperator != nil {
			t.Errorf("expected ingress configuration to be omitted by the older reader, got %+v", current.IngressOperator)
		}
		if current.ClusterNetworkOperator == nil || current.ClusterNetworkOperator.DisableMultiNetwork == nil || !*current.ClusterNetworkOperator.DisableMultiNetwork {
			t.Errorf("expected sibling cluster network configuration to survive, got %+v", current.ClusterNetworkOperator)
		}
	})
}
