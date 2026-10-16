package v1beta1

import (
	"encoding/json"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
)

// ingressOperatorSpecNMinus1 represents the previous version of IngressOperatorSpec
// without the DefaultCertificate field.
type ingressOperatorSpecNMinus1 struct {
	EndpointPublishingStrategy json.RawMessage `json:"endpointPublishingStrategy,omitempty"` //nolint:kubeapilinter
}

// awsCSIDriverConfigNMinus1 represents the previous version of AWSCSIDriverConfig
// without the InitialKMSKeyARN field.
type awsCSIDriverConfigNMinus1 struct{}

// csiDriverOperatorSpecNMinus1 represents the previous version of
// CSIDriverOperatorSpec without the AWS field.
type csiDriverOperatorSpecNMinus1 struct{}

func TestCSIDriverOperatorSpecSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		current      CSIDriverOperatorSpec
		expectedJSON string
	}{
		{
			name:         "When the spec is zero it should serialize to an empty object",
			current:      CSIDriverOperatorSpec{},
			expectedJSON: `{}`,
		},
		{
			name:         "When aws is set without a key it should omit initialKMSKeyARN",
			current:      CSIDriverOperatorSpec{AWS: AWSCSIDriverConfig{}},
			expectedJSON: `{}`,
		},
		{
			name: "When initialKMSKeyARN is set it should serialize nested under aws",
			current: CSIDriverOperatorSpec{
				AWS: AWSCSIDriverConfig{
					InitialKMSKeyARN: "arn:aws:kms:us-east-1:123456789012:key/abc-123",
				},
			},
			expectedJSON: `{"aws":{"initialKMSKeyARN":"arn:aws:kms:us-east-1:123456789012:key/abc-123"}}`,
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

			// N -> N-1: old code without the AWS field must ignore it and deserialize cleanly.
			var nMinus1 csiDriverOperatorSpecNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}

			// N-1 -> N: data from old code (no csiDriverOperator fields) must deserialize
			// into the new struct with a zero AWS config.
			nMinus1Data, err := json.Marshal(csiDriverOperatorSpecNMinus1{})
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTrip CSIDriverOperatorSpec
			if err := json.Unmarshal(nMinus1Data, &roundTrip); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTrip.AWS.InitialKMSKeyARN != "" {
				t.Errorf("expected AWS.InitialKMSKeyARN to be zero after N-1 round-trip, got %q", roundTrip.AWS.InitialKMSKeyARN)
			}

			// An N-1 AWSCSIDriverConfig (no initialKMSKeyARN) must deserialize into N
			// with a zero key, confirming the field addition is backward compatible.
			awsNMinus1Data, err := json.Marshal(awsCSIDriverConfigNMinus1{})
			if err != nil {
				t.Fatalf("failed to marshal N-1 aws struct: %v", err)
			}
			var awsRoundTrip AWSCSIDriverConfig
			if err := json.Unmarshal(awsNMinus1Data, &awsRoundTrip); err != nil {
				t.Fatalf("N failed to unmarshal aws JSON from N-1: %v", err)
			}
			if awsRoundTrip.InitialKMSKeyARN != "" {
				t.Errorf("expected AWSCSIDriverConfig.InitialKMSKeyARN to be zero after N-1 round-trip, got %q", awsRoundTrip.InitialKMSKeyARN)
			}
		})
	}
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
		{
			name:         "When N-1 data carries EndpointPublishingStrategy it should survive the round-trip into N",
			current:      IngressOperatorSpec{},
			expectedJSON: `{}`,
			nMinus1Result: ingressOperatorSpecNMinus1{
				EndpointPublishingStrategy: json.RawMessage(`{"type":"LoadBalancerService"}`),
			},
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

			// Sibling fields written by N-1 must survive into N unchanged; otherwise
			// the round-trip would silently drop data the enhancement requires to be
			// preserved.
			if len(tt.nMinus1Result.EndpointPublishingStrategy) > 0 {
				if roundTrip.EndpointPublishingStrategy == nil {
					t.Errorf("expected EndpointPublishingStrategy to survive N-1 -> N round-trip, got nil")
				} else if roundTrip.EndpointPublishingStrategy.Type != operatorv1.LoadBalancerServiceStrategyType {
					t.Errorf("expected EndpointPublishingStrategy.Type %q to survive N-1 -> N round-trip, got %q",
						operatorv1.LoadBalancerServiceStrategyType, roundTrip.EndpointPublishingStrategy.Type)
				}
			}
		})
	}
}
