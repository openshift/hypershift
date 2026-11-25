package v1beta1

import (
	"encoding/json"
	"testing"
)

// hostedControlPlaneStatusNMinus1 is a shape probe for the N-1 version of
// HostedControlPlaneStatus, before the sharedIngressWorkerAPIEndpoint field was
// added. It is used to verify that JSON produced by the current type can be
// deserialized by previous versions of the code, and vice versa.
type hostedControlPlaneStatusNMinus1 struct {
	// ready denotes that the HostedControlPlane API Server is ready to
	// receive requests.
	// +required
	//nolint:kubeapilinter // Preserve the previous API representation.
	Ready bool `json:"ready"`

	// initialized denotes whether or not the control plane has
	// provided a kubeadm-config.
	// +required
	//nolint:kubeapilinter // Preserve the previous API representation.
	Initialized bool `json:"initialized"`

	// controlPlaneEndpoint contains the endpoint information by which
	// external clients can access the control plane.
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`
}

func TestHostedControlPlaneStatusSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       HostedControlPlaneStatus
		expectedJSON  string
		nMinus1Result hostedControlPlaneStatusNMinus1
	}{
		{
			name: "When the endpoint is published it should serialize and N-1 should ignore unknown field",
			current: HostedControlPlaneStatus{
				Ready:                          true,
				Initialized:                    true,
				ControlPlaneEndpoint:           APIEndpoint{Host: "api.cluster.example.com", Port: 6443},
				SharedIngressWorkerAPIEndpoint: &APIEndpoint{Host: "10.0.0.1", Port: 6443},
			},
			expectedJSON: `{"ready":true,"initialized":true,"controlPlaneEndpoint":{"host":"api.cluster.example.com","port":6443},"sharedIngressWorkerAPIEndpoint":{"host":"10.0.0.1","port":6443}}`,
			nMinus1Result: hostedControlPlaneStatusNMinus1{
				Ready:                true,
				Initialized:          true,
				ControlPlaneEndpoint: APIEndpoint{Host: "api.cluster.example.com", Port: 6443},
			},
		},
		{
			name: "When the endpoint is nil it should be omitted and N-1 should deserialize normally",
			current: HostedControlPlaneStatus{
				Ready:                true,
				Initialized:          true,
				ControlPlaneEndpoint: APIEndpoint{Host: "api.cluster.example.com", Port: 6443},
			},
			expectedJSON: `{"ready":true,"initialized":true,"controlPlaneEndpoint":{"host":"api.cluster.example.com","port":6443}}`,
			nMinus1Result: hostedControlPlaneStatusNMinus1{
				Ready:                true,
				Initialized:          true,
				ControlPlaneEndpoint: APIEndpoint{Host: "api.cluster.example.com", Port: 6443},
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
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			// Deserialize into N-1 struct
			var nMinus1 hostedControlPlaneStatusNMinus1
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
			var roundTripped HostedControlPlaneStatus
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Ready != tt.nMinus1Result.Ready ||
				roundTripped.Initialized != tt.nMinus1Result.Initialized ||
				roundTripped.ControlPlaneEndpoint != tt.nMinus1Result.ControlPlaneEndpoint {
				t.Errorf("existing status fields mismatch after N-1 round-trip: got %+v, want %+v", roundTripped, tt.nMinus1Result)
			}
			if roundTripped.SharedIngressWorkerAPIEndpoint != nil {
				t.Errorf("sharedIngressWorkerAPIEndpoint should be nil after N-1 round-trip, got %+v", roundTripped.SharedIngressWorkerAPIEndpoint)
			}
		})
	}
}
