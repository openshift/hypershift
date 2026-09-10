package v1beta1

import (
	"encoding/json"
	"testing"
)

type controlPlaneComponentConfigurationNMinus1 struct{}

func TestControlPlaneComponentConfigurationSerializationCompatibility(t *testing.T) {
	current := ControlPlaneComponentConfiguration{
		Router: ControlPlaneWorkloadConfiguration{Replicas: 2},
	}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("failed to marshal current struct: %v", err)
	}

	var nMinus1 controlPlaneComponentConfigurationNMinus1
	if err := json.Unmarshal(data, &nMinus1); err != nil {
		t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
	}

	oldData, err := json.Marshal(nMinus1)
	if err != nil {
		t.Fatalf("failed to marshal N-1 struct: %v", err)
	}
	var roundTripped ControlPlaneComponentConfiguration
	if err := json.Unmarshal(oldData, &roundTripped); err != nil {
		t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
	}
	if roundTripped.Router.Replicas != 0 {
		t.Fatalf("Router replicas should be zero after unmarshalling N-1 data, got %v", roundTripped.Router.Replicas)
	}
}
