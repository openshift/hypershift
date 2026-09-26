package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestHostedClusterLabelsSerializationCompatibility(t *testing.T) {
	testLabelsSerializationCompatibility(t, "HostedCluster")
}

func TestHostedControlPlaneLabelsSerializationCompatibility(t *testing.T) {
	testLabelsSerializationCompatibility(t, "HostedControlPlane")
}

func testLabelsSerializationCompatibility(t *testing.T, resourceName string) {
	tests := []struct {
		name                   string
		currentLabels          map[string]LabelValue
		oldLabels              map[string]string
		expectedLabelsJSON     string
		expectedOldLabels      map[string]string
		expectedCurrentFromOld map[string]LabelValue
	}{
		{
			name:                   "When labels are nil it should be omitted",
			currentLabels:          nil,
			oldLabels:              nil,
			expectedLabelsJSON:     "",
			expectedOldLabels:      nil,
			expectedCurrentFromOld: nil,
		},
		{
			name:                   "When labels are empty it should be omitted",
			currentLabels:          map[string]LabelValue{},
			oldLabels:              map[string]string{},
			expectedLabelsJSON:     "",
			expectedOldLabels:      nil,
			expectedCurrentFromOld: nil,
		},
		{
			name:                   "When labels are populated it should preserve the JSON representation",
			currentLabels:          map[string]LabelValue{"team": "platform"},
			oldLabels:              map[string]string{"team": "platform"},
			expectedLabelsJSON:     `{"team":"platform"}`,
			expectedOldLabels:      map[string]string{"team": "platform"},
			expectedCurrentFromOld: map[string]LabelValue{"team": "platform"},
		},
	}

	for _, tt := range tests {
		t.Run(resourceName+" "+tt.name, func(t *testing.T) {
			var data []byte
			var err error
			switch resourceName {
			case "HostedCluster":
				data, err = json.Marshal(HostedClusterSpec{Labels: tt.currentLabels})
			case "HostedControlPlane":
				data, err = json.Marshal(HostedControlPlaneSpec{Labels: tt.currentLabels})
			default:
				t.Fatalf("unsupported resource %s", resourceName)
			}
			if err != nil {
				t.Fatalf("failed to marshal current %s labels: %v", resourceName, err)
			}

			var currentFields map[string]json.RawMessage
			if err := json.Unmarshal(data, &currentFields); err != nil {
				t.Fatalf("failed to inspect current %s JSON: %v", resourceName, err)
			}
			labelsJSON, labelsPresent := currentFields["labels"]
			if tt.expectedLabelsJSON == "" {
				if labelsPresent {
					t.Fatalf("expected current %s labels to be omitted, got %s", resourceName, string(labelsJSON))
				}
			} else if string(labelsJSON) != tt.expectedLabelsJSON {
				t.Fatalf("unexpected current %s labels JSON: got %s, want %s", resourceName, string(labelsJSON), tt.expectedLabelsJSON)
			}

			var old struct {
				Labels map[string]string `json:"labels,omitempty"`
			}
			if err := json.Unmarshal(data, &old); err != nil {
				t.Fatalf("old %s shape failed to unmarshal current JSON: %v", resourceName, err)
			}
			if !reflect.DeepEqual(old.Labels, tt.expectedOldLabels) {
				t.Fatalf("old %s labels mismatch: got %#v, want %#v", resourceName, old.Labels, tt.expectedOldLabels)
			}

			oldData, err := json.Marshal(struct {
				Labels map[string]string `json:"labels,omitempty"`
			}{Labels: tt.oldLabels})
			if err != nil {
				t.Fatalf("failed to marshal old %s labels: %v", resourceName, err)
			}
			var currentLabels map[string]LabelValue
			switch resourceName {
			case "HostedCluster":
				var current HostedClusterSpec
				if err := json.Unmarshal(oldData, &current); err != nil {
					t.Fatalf("current %s shape failed to unmarshal old JSON: %v", resourceName, err)
				}
				currentLabels = current.Labels
			case "HostedControlPlane":
				var current HostedControlPlaneSpec
				if err := json.Unmarshal(oldData, &current); err != nil {
					t.Fatalf("current %s shape failed to unmarshal old JSON: %v", resourceName, err)
				}
				currentLabels = current.Labels
			}
			if !reflect.DeepEqual(currentLabels, tt.expectedCurrentFromOld) {
				t.Fatalf("current %s labels mismatch after old-to-current conversion: got %#v, want %#v", resourceName, currentLabels, tt.expectedCurrentFromOld)
			}
		})
	}

	t.Run(resourceName+" omitted labels should decode as nil", func(t *testing.T) {
		var currentLabels map[string]LabelValue
		switch resourceName {
		case "HostedCluster":
			var current HostedClusterSpec
			if err := json.Unmarshal([]byte(`{}`), &current); err != nil {
				t.Fatalf("failed to decode omitted labels: %v", err)
			}
			currentLabels = current.Labels
		case "HostedControlPlane":
			var current HostedControlPlaneSpec
			if err := json.Unmarshal([]byte(`{}`), &current); err != nil {
				t.Fatalf("failed to decode omitted labels: %v", err)
			}
			currentLabels = current.Labels
		}
		if currentLabels != nil {
			t.Fatalf("expected omitted labels to decode as nil, got %#v", currentLabels)
		}
	})

	t.Run(resourceName+" explicit empty labels should decode as an empty map", func(t *testing.T) {
		var currentLabels map[string]LabelValue
		switch resourceName {
		case "HostedCluster":
			var current HostedClusterSpec
			if err := json.Unmarshal([]byte(`{"labels":{}}`), &current); err != nil {
				t.Fatalf("failed to decode explicit empty labels: %v", err)
			}
			currentLabels = current.Labels
		case "HostedControlPlane":
			var current HostedControlPlaneSpec
			if err := json.Unmarshal([]byte(`{"labels":{}}`), &current); err != nil {
				t.Fatalf("failed to decode explicit empty labels: %v", err)
			}
			currentLabels = current.Labels
		}
		if currentLabels == nil || len(currentLabels) != 0 {
			t.Fatalf("expected explicit empty labels to decode as a non-nil empty map, got %#v", currentLabels)
		}
	})
}
