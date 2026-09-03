package v1beta1

import (
	"encoding/json"
	"testing"
)

// hostedClusterSpecNMinus1 represents the N-1 (previous) version of the relevant
// subset of HostedClusterSpec, before controlPlaneAvailabilityZoneScheduling was
// added. It is used to verify that JSON produced by the current type can be
// deserialized by previous versions of the code, and vice versa.
type hostedClusterSpecNMinus1 struct {
	// controllerAvailabilityPolicy mirrors the previous-version field used to prove
	// serialization compatibility with the new controlPlaneAvailabilityZoneScheduling field.
	// +optional
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`
}

func TestControlPlaneAvailabilityZoneSchedulingSerializationCompatibility(t *testing.T) {
	t.Run("current type omits the field when unset (omitzero) so N-1 sees zero value", func(t *testing.T) {
		current := HostedClusterSpec{ControllerAvailabilityPolicy: HighlyAvailable}
		data, err := json.Marshal(current)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// omitzero: an unset struct field must not be serialized.
		if got := string(data); containsSubstring(got, "controlPlaneAvailabilityZoneScheduling") {
			t.Fatalf("expected controlPlaneAvailabilityZoneScheduling to be omitted when unset, got %s", got)
		}
		// N-1 can deserialize N (forward-compat, field absent).
		var nMinus1 hostedClusterSpecNMinus1
		if err := json.Unmarshal(data, &nMinus1); err != nil {
			t.Fatalf("N-1 unmarshal of N data: %v", err)
		}
		if nMinus1.ControllerAvailabilityPolicy != HighlyAvailable {
			t.Fatalf("unexpected N-1 value: %+v", nMinus1)
		}
	})

	t.Run("N-1 data (field absent) deserializes into current type as zero value", func(t *testing.T) {
		nMinus1 := hostedClusterSpecNMinus1{ControllerAvailabilityPolicy: HighlyAvailable}
		data, err := json.Marshal(nMinus1)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var current HostedClusterSpec
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatalf("N unmarshal of N-1 data: %v", err)
		}
		if current.ControlPlaneAvailabilityZoneScheduling.Policy != "" {
			t.Fatalf("expected zero-value policy, got %q", current.ControlPlaneAvailabilityZoneScheduling.Policy)
		}
	})

	t.Run("current type round-trips a set field and N-1 ignores it", func(t *testing.T) {
		current := HostedClusterSpec{
			ControllerAvailabilityPolicy: HighlyAvailable,
			ControlPlaneAvailabilityZoneScheduling: ControlPlaneAvailabilityZoneScheduling{
				Policy:            ControlPlaneAvailabilityZoneSchedulingMinimal,
				NonZonalPlacement: NonZonalPlacementRequired,
			},
		}
		data, err := json.Marshal(current)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var roundTripped HostedClusterSpec
		if err := json.Unmarshal(data, &roundTripped); err != nil {
			t.Fatalf("round-trip unmarshal: %v", err)
		}
		if roundTripped.ControlPlaneAvailabilityZoneScheduling != current.ControlPlaneAvailabilityZoneScheduling {
			t.Fatalf("round-trip mismatch: %+v vs %+v", roundTripped.ControlPlaneAvailabilityZoneScheduling, current.ControlPlaneAvailabilityZoneScheduling)
		}
		// N-1 tolerates the unknown field.
		var nMinus1 hostedClusterSpecNMinus1
		if err := json.Unmarshal(data, &nMinus1); err != nil {
			t.Fatalf("N-1 unmarshal of N data with field set: %v", err)
		}
	})
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
