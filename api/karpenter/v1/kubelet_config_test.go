package v1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/utils/ptr"
)

func TestKubeletConfigurationMarshalRoundTrip(t *testing.T) {
	testCases := []struct {
		name   string
		config KubeletConfiguration
	}{
		{
			name: "When all typed fields are set they should round-trip",
			config: KubeletConfiguration{
				MaxPods:     110,
				PodsPerCore: 10,
				SystemReserved: map[string]string{
					"cpu":    "100m",
					"memory": "256Mi",
				},
				KubeReserved: map[string]string{
					"cpu":    "200m",
					"memory": "512Mi",
				},
				EvictionHard: map[string]EvictionThreshold{
					"memory.available": "100Mi",
				},
				EvictionSoft: map[string]EvictionThreshold{
					"memory.available": "200Mi",
				},
				EvictionSoftGracePeriod: map[string]string{
					"memory.available": "30s",
				},
				EvictionMaxPodGracePeriod:   ptr.To(int32(60)),
				ImageGCHighThresholdPercent: ptr.To(int32(85)),
				ImageGCLowThresholdPercent:  ptr.To(int32(80)),
				CPUCFSQuota:                 ptr.To(true),
			},
		},
		{
			name: "When only some fields are set they should round-trip",
			config: KubeletConfiguration{
				MaxPods:     50,
				CPUCFSQuota: ptr.To(false),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.config)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var roundTripped KubeletConfiguration
			if err := json.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if !reflect.DeepEqual(tc.config, roundTripped) {
				t.Errorf("round-trip mismatch:\n  original:     %+v\n  round-tripped: %+v", tc.config, roundTripped)
			}
		})
	}
}

func TestKubeletConfigurationOverflowPreservation(t *testing.T) {
	t.Run("When unknown fields are present they should be preserved through marshal/unmarshal", func(t *testing.T) {
		input := `{
			"maxPods": 110,
			"registryPullQPS": 5,
			"registryBurst": 10,
			"containerLogMaxSize": "10Mi",
			"podPidsLimit": 4096
		}`

		var config KubeletConfiguration
		if err := json.Unmarshal([]byte(input), &config); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Typed field should be populated
		if config.MaxPods != 110 {
			t.Errorf("expected MaxPods=110, got %v", config.MaxPods)
		}

		// Marshal back
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		// Verify overflow fields are present in output
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal to raw map: %v", err)
		}

		for _, key := range []string{"maxPods", "registryPullQPS", "registryBurst", "containerLogMaxSize", "podPidsLimit"} {
			if _, ok := raw[key]; !ok {
				t.Errorf("expected key %q to be present in marshaled output", key)
			}
		}
	})

	t.Run("When only overflow fields are present they should round-trip", func(t *testing.T) {
		input := `{"registryPullQPS": 5, "registryBurst": 10}`

		var config KubeletConfiguration
		if err := json.Unmarshal([]byte(input), &config); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal to raw map: %v", err)
		}

		if len(raw) != 2 {
			t.Errorf("expected 2 keys, got %d: %v", len(raw), raw)
		}
		if _, ok := raw["registryPullQPS"]; !ok {
			t.Error("expected registryPullQPS in output")
		}
		if _, ok := raw["registryBurst"]; !ok {
			t.Error("expected registryBurst in output")
		}
	})

	t.Run("When a structured field conflicts with an overflow field the structured field should win", func(t *testing.T) {
		input := `{"maxPods": 110, "registryPullQPS": 5}`
		var config KubeletConfiguration
		if err := json.Unmarshal([]byte(input), &config); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Simulate a stale overflow that still contains maxPods from a prior serialization
		config.Overflow.Raw = []byte(`{"maxPods": 999, "registryPullQPS": 5}`)
		config.MaxPods = 42

		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal to raw map: %v", err)
		}

		if string(raw["maxPods"]) != "42" {
			t.Errorf("expected structured maxPods=42 to win, got %s", raw["maxPods"])
		}
		if _, ok := raw["registryPullQPS"]; !ok {
			t.Error("expected overflow field registryPullQPS to still be present")
		}
	})

	t.Run("When there are no overflow fields the output should contain only typed fields", func(t *testing.T) {
		config := KubeletConfiguration{
			MaxPods: 110,
		}

		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal to raw map: %v", err)
		}

		if len(raw) != 1 {
			t.Errorf("expected 1 key, got %d: %v", len(raw), raw)
		}
	})
}

func TestKubeletConfigurationDeepCopy(t *testing.T) {
	t.Run("When overflow fields are present DeepCopy should preserve them", func(t *testing.T) {
		input := `{"maxPods": 110, "registryPullQPS": 5}`

		var original KubeletConfiguration
		if err := json.Unmarshal([]byte(input), &original); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		copied := original.DeepCopy()

		// Verify typed fields are copied
		if copied.MaxPods != 110 {
			t.Errorf("expected MaxPods=110, got %v", copied.MaxPods)
		}

		// Verify overflow is independent (modifying copy shouldn't affect original)
		copied.MaxPods = 200

		if original.MaxPods != 110 {
			t.Error("modifying copy affected original MaxPods")
		}

		// Verify overflow survives marshal
		data, err := json.Marshal(copied)
		if err != nil {
			t.Fatalf("failed to marshal copy: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal to raw map: %v", err)
		}
		if _, ok := raw["registryPullQPS"]; !ok {
			t.Error("overflow field registryPullQPS not preserved in deep copy")
		}
	})

	t.Run("When KubeletConfiguration is nil DeepCopy should return nil", func(t *testing.T) {
		var nilConfig *KubeletConfiguration
		if nilConfig.DeepCopy() != nil {
			t.Error("expected nil from DeepCopy of nil")
		}
	})
}
