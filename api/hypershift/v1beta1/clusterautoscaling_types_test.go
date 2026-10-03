package v1beta1

import (
	"encoding/json"
	"testing"

	"k8s.io/utils/ptr"
)

// clusterAutoscalingNMinus1 represents ClusterAutoscaling before kubeClientQPS
// and kubeClientBurst were added. Used to verify N-1 / N+1 JSON compatibility.
type clusterAutoscalingNMinus1 struct {
	// maxNodesTotal is retained so N-1 round-trips preserve an existing field.
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"` //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestClusterAutoscalingSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       ClusterAutoscaling
		expectedJSON  string
		nMinus1Result clusterAutoscalingNMinus1
	}{
		{
			name: "When kubeClientQPS and kubeClientBurst are set it should round-trip to N-1 without those fields",
			current: ClusterAutoscaling{
				MaxNodesTotal:   ptr.To[int32](100),
				KubeClientQPS:   ptr.To[int32](50),
				KubeClientBurst: 100,
			},
			expectedJSON: `{"maxNodesTotal":100,"kubeClientQPS":50,"kubeClientBurst":100}`,
			nMinus1Result: clusterAutoscalingNMinus1{
				MaxNodesTotal: ptr.To[int32](100),
			},
		},
		{
			name: "When kubeClientQPS and kubeClientBurst are nil/zero it should omit them from JSON",
			current: ClusterAutoscaling{
				MaxNodesTotal: ptr.To[int32](10),
			},
			expectedJSON: `{"maxNodesTotal":10}`,
			nMinus1Result: clusterAutoscalingNMinus1{
				MaxNodesTotal: ptr.To[int32](10),
			},
		},
		{
			name: "When only kubeClientQPS is set to -1 it should serialize that field alone",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](-1),
			},
			expectedJSON:  `{"kubeClientQPS":-1}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientQPS is set to 0 it should serialize 0 and not omit the field",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](0),
			},
			expectedJSON:  `{"kubeClientQPS":0}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientQPS is set to 1000 it should serialize the maximum value",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](1000),
			},
			expectedJSON:  `{"kubeClientQPS":1000}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When only kubeClientBurst is set it should serialize that field alone",
			current: ClusterAutoscaling{
				KubeClientBurst: 200,
			},
			expectedJSON:  `{"kubeClientBurst":200}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientBurst is set to 1 it should serialize the minimum value",
			current: ClusterAutoscaling{
				KubeClientBurst: 1,
			},
			expectedJSON:  `{"kubeClientBurst":1}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientBurst is set to 2000 it should serialize the maximum value",
			current: ClusterAutoscaling{
				KubeClientBurst: 2000,
			},
			expectedJSON:  `{"kubeClientBurst":2000}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
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

			var nMinus1 clusterAutoscalingNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if ptr.Deref(nMinus1.MaxNodesTotal, -1) != ptr.Deref(tt.nMinus1Result.MaxNodesTotal, -1) {
				t.Errorf("N-1 MaxNodesTotal mismatch: got %v, want %v", nMinus1.MaxNodesTotal, tt.nMinus1Result.MaxNodesTotal)
			}

			// Reverse: N-1 JSON (without new fields) deserializes into current with nil/zero.
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped ClusterAutoscaling
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.KubeClientQPS != nil {
				t.Errorf("KubeClientQPS should be nil after N-1 round-trip, got %v", *roundTripped.KubeClientQPS)
			}
			if roundTripped.KubeClientBurst != 0 {
				t.Errorf("KubeClientBurst should be 0 after N-1 round-trip, got %v", roundTripped.KubeClientBurst)
			}
			if ptr.Deref(roundTripped.MaxNodesTotal, -1) != ptr.Deref(tt.nMinus1Result.MaxNodesTotal, -1) {
				t.Errorf("MaxNodesTotal mismatch after N-1 round-trip: got %v, want %v", roundTripped.MaxNodesTotal, tt.nMinus1Result.MaxNodesTotal)
			}
		})
	}
}
