package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// kubevirtComputeNMinus1 represents the previous version of KubevirtCompute
// before the Model field was added. It is used to verify that JSON produced
// by the current type can be deserialized by previous versions of the code,
// and vice versa.
//
//nolint:kubeapilinter // N-1 compatibility fixture; intentionally mirrors the previous API version
type kubevirtComputeNMinus1 struct {
	Memory   *resource.Quantity `json:"memory,omitempty"`
	Cores    *uint32            `json:"cores,omitempty"`
	QosClass *QoSClass          `json:"qosClass,omitempty"`
}

func TestKubevirtComputeSerializationCompatibility(t *testing.T) {
	burstable := QoSClassBurstable
	guaranteed := QoSClassGuaranteed
	mem8Gi := resource.MustParse("8Gi")

	tests := []struct {
		name          string
		current       KubevirtCompute
		expectedJSON  string
		nMinus1Result kubevirtComputeNMinus1
	}{
		{
			name:          "When all fields are zero-value, it should round-trip as empty object",
			current:       KubevirtCompute{},
			expectedJSON:  `{}`,
			nMinus1Result: kubevirtComputeNMinus1{},
		},
		{
			name: "When Model is empty, it should be omitted and N-1 should deserialize without it",
			current: KubevirtCompute{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](4),
				QosClass: &burstable,
			},
			expectedJSON: `{"memory":"8Gi","cores":4,"qosClass":"Burstable"}`,
			nMinus1Result: kubevirtComputeNMinus1{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](4),
				QosClass: &burstable,
			},
		},
		{
			name: "When Model is set to HostPassthrough, it should be included and N-1 should ignore it",
			current: KubevirtCompute{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](4),
				QosClass: &burstable,
				Model:    CpuModelHostPassthrough,
			},
			expectedJSON: `{"memory":"8Gi","cores":4,"qosClass":"Burstable","model":"HostPassthrough"}`,
			nMinus1Result: kubevirtComputeNMinus1{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](4),
				QosClass: &burstable,
			},
		},
		{
			name: "When only Model is set, it should serialize correctly and N-1 should get empty fields",
			current: KubevirtCompute{
				Model: CpuModelHostPassthrough,
			},
			expectedJSON:  `{"model":"HostPassthrough"}`,
			nMinus1Result: kubevirtComputeNMinus1{},
		},
		{
			name: "When Model is set with Guaranteed QoS, it should round-trip correctly",
			current: KubevirtCompute{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](8),
				QosClass: &guaranteed,
				Model:    CpuModelHostPassthrough,
			},
			expectedJSON: `{"memory":"8Gi","cores":8,"qosClass":"Guaranteed","model":"HostPassthrough"}`,
			nMinus1Result: kubevirtComputeNMinus1{
				Memory:   &mem8Gi,
				Cores:    ptr.To[uint32](8),
				QosClass: &guaranteed,
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

			var nMinus1 kubevirtComputeNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if !reflect.DeepEqual(nMinus1, tt.nMinus1Result) {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped KubevirtCompute
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if !reflect.DeepEqual(roundTripped.Memory, tt.nMinus1Result.Memory) {
				t.Errorf("Memory mismatch after N-1 round-trip: got %v, want %v", roundTripped.Memory, tt.nMinus1Result.Memory)
			}
			if !reflect.DeepEqual(roundTripped.Cores, tt.nMinus1Result.Cores) {
				t.Errorf("Cores mismatch after N-1 round-trip: got %v, want %v", roundTripped.Cores, tt.nMinus1Result.Cores)
			}
			if !reflect.DeepEqual(roundTripped.QosClass, tt.nMinus1Result.QosClass) {
				t.Errorf("QosClass mismatch after N-1 round-trip: got %v, want %v", roundTripped.QosClass, tt.nMinus1Result.QosClass)
			}
			if roundTripped.Model != "" {
				t.Errorf("Model should be empty after N-1 round-trip: got %v", roundTripped.Model)
			}
		})
	}
}
