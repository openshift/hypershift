package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/utils/ptr"
)

// These types represent the N-1 (previous) version of the API structs,
// before the int32 -> *int32 pointer change. They are used to verify
// that JSON produced by the current types can be deserialized by
// previous versions of the code, and vice versa.
type nodePoolAutoScalingNMinus1 struct {
	Min int32 `json:"min"`
	Max int32 `json:"max"`
}

func TestNodePoolAutoScalingSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// current is the N (current) version of the struct
		current NodePoolAutoScaling
		// expectedJSON is the expected JSON output from marshaling current
		expectedJSON string
		// nMinus1Result is the expected result when unmarshaling into the N-1 struct
		nMinus1Result nodePoolAutoScalingNMinus1
	}{
		{
			name: "When Min is set to a positive value it should round-trip to N-1",
			current: NodePoolAutoScaling{
				Min: ptr.To[int32](3),
				Max: 5,
			},
			expectedJSON:  `{"min":3,"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 3, Max: 5},
		},
		{
			name: "When Min is explicitly zero it should round-trip to N-1",
			current: NodePoolAutoScaling{
				Min: ptr.To[int32](0),
				Max: 5,
			},
			expectedJSON:  `{"min":0,"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 0, Max: 5},
		},
		{
			name: "When Min is nil it should be omitted and N-1 should deserialize as zero value",
			current: NodePoolAutoScaling{
				Min: nil,
				Max: 5,
			},
			expectedJSON:  `{"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 0, Max: 5},
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
			var nMinus1 nodePoolAutoScalingNMinus1
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
			var roundTripped NodePoolAutoScaling
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Max != tt.nMinus1Result.Max {
				t.Errorf("Max mismatch after N-1 round-trip: got %d, want %d", roundTripped.Max, tt.nMinus1Result.Max)
			}
			if ptr.Deref(roundTripped.Min, -1) != tt.nMinus1Result.Min {
				t.Errorf("Min mismatch after N-1 round-trip: got %v, want %d", roundTripped.Min, tt.nMinus1Result.Min)
			}
		})
	}
}

func TestKubevirtEvictionStrategySerializationCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		current      KubevirtNodePoolPlatform
		expectedJSON string
	}{
		{
			name: "When EvictionStrategy is not set it should be omitted from JSON",
			current: KubevirtNodePoolPlatform{
				RootVolume: &KubevirtRootVolume{},
			},
			expectedJSON: `{"rootVolume":{}}`,
		},
		{
			name: "When EvictionStrategy is External it should be preserved",
			current: KubevirtNodePoolPlatform{
				RootVolume:       &KubevirtRootVolume{},
				EvictionStrategy: EvictionStrategyExternal,
			},
			expectedJSON: `{"rootVolume":{},"evictionStrategy":"External"}`,
		},
		{
			name: "When EvictionStrategy is LiveMigrate it should be preserved",
			current: KubevirtNodePoolPlatform{
				RootVolume:       &KubevirtRootVolume{},
				EvictionStrategy: EvictionStrategyLiveMigrate,
			},
			expectedJSON: `{"rootVolume":{},"evictionStrategy":"LiveMigrate"}`,
		},
		{
			name: "When EvictionStrategy is None it should be preserved",
			current: KubevirtNodePoolPlatform{
				RootVolume:       &KubevirtRootVolume{},
				EvictionStrategy: EvictionStrategyNone,
			},
			expectedJSON: `{"rootVolume":{},"evictionStrategy":"None"}`,
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

			// Round-trip: unmarshal back into current type
			var roundTripped KubevirtNodePoolPlatform
			if err := json.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("failed to round-trip unmarshal: %v", err)
			}
			if roundTripped.EvictionStrategy != tt.current.EvictionStrategy {
				t.Errorf("EvictionStrategy mismatch after round-trip: got %q, want %q", roundTripped.EvictionStrategy, tt.current.EvictionStrategy)
			}

			// N-1 compatibility: verify current JSON can be parsed by code
			// that doesn't know about evictionStrategy (unknown fields ignored)
			var rawMap map[string]json.RawMessage
			if err := json.Unmarshal(data, &rawMap); err != nil {
				t.Fatalf("failed to unmarshal into raw map: %v", err)
			}

			// N-1 JSON (without evictionStrategy) deserializes into current struct
			delete(rawMap, "evictionStrategy")
			nMinus1Data, err := json.Marshal(rawMap)
			if err != nil {
				t.Fatalf("failed to marshal N-1 map: %v", err)
			}
			var fromNMinus1 KubevirtNodePoolPlatform
			if err := json.Unmarshal(nMinus1Data, &fromNMinus1); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if fromNMinus1.EvictionStrategy != "" {
				t.Errorf("EvictionStrategy should be zero value when deserialized from N-1, got %q", fromNMinus1.EvictionStrategy)
			}
		})
	}
}

func TestKubevirtEvictionStrategyEnumValues(t *testing.T) {
	expected := []KubevirtEvictionStrategy{
		EvictionStrategyLiveMigrate,
		EvictionStrategyLiveMigrateIfPossible,
		EvictionStrategyExternal,
		EvictionStrategyNone,
	}

	for _, strategy := range expected {
		t.Run("When EvictionStrategy is "+string(strategy)+" it should round-trip through JSON", func(t *testing.T) {
			src := KubevirtNodePoolPlatform{
				RootVolume:       &KubevirtRootVolume{},
				EvictionStrategy: strategy,
			}
			data, err := json.Marshal(src)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}
			var dst KubevirtNodePoolPlatform
			if err := json.Unmarshal(data, &dst); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if dst.EvictionStrategy != strategy {
				t.Errorf("got %q, want %q", dst.EvictionStrategy, strategy)
			}
		})
	}
}

func TestKubevirtNodePoolPlatformDeepCopyPreservesEvictionStrategy(t *testing.T) {
	original := &KubevirtNodePoolPlatform{
		RootVolume:       &KubevirtRootVolume{},
		EvictionStrategy: EvictionStrategyExternal,
	}
	copied := original.DeepCopy()
	if !reflect.DeepEqual(original, copied) {
		t.Errorf("DeepCopy mismatch: got %+v, want %+v", copied, original)
	}
	// Mutate copy, verify original unchanged
	copied.EvictionStrategy = EvictionStrategyNone
	if original.EvictionStrategy != EvictionStrategyExternal {
		t.Error("mutating DeepCopy affected original")
	}
}
