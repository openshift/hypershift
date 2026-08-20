package v1beta1

import (
	"encoding/json"
	"testing"
)

// These types represent the N-1 (previous) version of the API structs,
// before the DNSZones field was added. They are used to verify that JSON
// produced by the current types can be deserialized by previous versions
// of the code, and vice versa.
type awsPlatformStatusNMinus1 struct {
	DefaultWorkerSecurityGroupID string `json:"defaultWorkerSecurityGroupID,omitempty"`
}

type awsEndpointServiceStatusNMinus1 struct {
	EndpointServiceName string `json:"endpointServiceName,omitempty"`
	EndpointID          string `json:"endpointID,omitempty"`
	DNSZoneID           string `json:"dnsZoneID,omitempty"`
	SecurityGroupID     string `json:"securityGroupID,omitempty"`
}

func TestAWSPlatformStatusSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AWSPlatformStatus
		expectedJSON  string
		nMinus1Result awsPlatformStatusNMinus1
	}{
		{
			name: "When DNSZones is set it should serialize and N-1 should ignore unknown field",
			current: AWSPlatformStatus{
				DefaultWorkerSecurityGroupID: "sg-123",
				DNSZones: []AWSDNSZoneStatus{
					{ZoneID: "Z123", ZoneType: PublicIngressZone, Name: "in.test.example.com"},
				},
			},
			expectedJSON:  `{"defaultWorkerSecurityGroupID":"sg-123","dnsZones":[{"zoneID":"Z123","zoneType":"PublicIngress","name":"in.test.example.com"}]}`,
			nMinus1Result: awsPlatformStatusNMinus1{DefaultWorkerSecurityGroupID: "sg-123"},
		},
		{
			name: "When DNSZones is nil it should be omitted and N-1 should deserialize normally",
			current: AWSPlatformStatus{
				DefaultWorkerSecurityGroupID: "sg-456",
			},
			expectedJSON:  `{"defaultWorkerSecurityGroupID":"sg-456"}`,
			nMinus1Result: awsPlatformStatusNMinus1{DefaultWorkerSecurityGroupID: "sg-456"},
		},
		{
			name:          "When all fields are empty it should produce empty JSON",
			current:       AWSPlatformStatus{},
			expectedJSON:  `{}`,
			nMinus1Result: awsPlatformStatusNMinus1{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal current (N) version
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			// Deserialize into N-1 struct
			var nMinus1 awsPlatformStatusNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1 != tt.nMinus1Result {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			// Reverse: marshal N-1 and deserialize into current (N)
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSPlatformStatus
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.DefaultWorkerSecurityGroupID != tt.nMinus1Result.DefaultWorkerSecurityGroupID {
				t.Errorf("DefaultWorkerSecurityGroupID mismatch after N-1 round-trip: got %q, want %q",
					roundTripped.DefaultWorkerSecurityGroupID, tt.nMinus1Result.DefaultWorkerSecurityGroupID)
			}
			if roundTripped.DNSZones != nil {
				t.Errorf("DNSZones should be nil after N-1 round-trip, got %+v", roundTripped.DNSZones)
			}
		})
	}
}

func TestAWSEndpointServiceStatusSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AWSEndpointServiceStatus
		nMinus1Result awsEndpointServiceStatusNMinus1
	}{
		{
			name: "When new fields are set N-1 should ignore them and preserve existing fields",
			current: AWSEndpointServiceStatus{
				EndpointServiceName: "com.amazonaws.vpce-svc-123",
				EndpointID:          "vpce-123",
				DNSZoneID:           "Z123",
				SecurityGroupID:     "sg-123",
				ManagedLocalZone:    "Managed",
			},
			nMinus1Result: awsEndpointServiceStatusNMinus1{
				EndpointServiceName: "com.amazonaws.vpce-svc-123",
				EndpointID:          "vpce-123",
				DNSZoneID:           "Z123",
				SecurityGroupID:     "sg-123",
			},
		},
		{
			name: "When new fields are empty they should be omitted",
			current: AWSEndpointServiceStatus{
				EndpointServiceName: "com.amazonaws.vpce-svc-456",
				DNSZoneID:           "Z456",
			},
			nMinus1Result: awsEndpointServiceStatusNMinus1{
				EndpointServiceName: "com.amazonaws.vpce-svc-456",
				DNSZoneID:           "Z456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal current (N) version
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}

			// Deserialize into N-1 struct (should ignore new fields)
			var nMinus1 awsEndpointServiceStatusNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1 != tt.nMinus1Result {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			// Reverse: marshal N-1 and deserialize into current (N)
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSEndpointServiceStatus
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.EndpointServiceName != tt.nMinus1Result.EndpointServiceName {
				t.Errorf("EndpointServiceName mismatch: got %q, want %q",
					roundTripped.EndpointServiceName, tt.nMinus1Result.EndpointServiceName)
			}
			if roundTripped.DNSZoneID != tt.nMinus1Result.DNSZoneID {
				t.Errorf("DNSZoneID mismatch: got %q, want %q",
					roundTripped.DNSZoneID, tt.nMinus1Result.DNSZoneID)
			}
			// New fields should be zero-valued after N-1 round-trip
			if roundTripped.ManagedLocalZone != "" {
				t.Errorf("ManagedLocalZone should be empty after N-1 round-trip, got %q", roundTripped.ManagedLocalZone)
			}
		})
	}
}
