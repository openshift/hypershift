package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Validation changed, but the GCPResourceTag wire representation must remain
// readable by clients built against the earlier API-only version of the type.
type gcpResourceTagNMinus1 struct {
	// key is the tag's short key in the previous wire representation.
	// +kubebuilder:validation:MaxLength=63
	Key string `json:"key,omitempty"`
	// value is the tag's short value in the previous wire representation.
	// +kubebuilder:validation:MaxLength=63
	Value string `json:"value,omitempty"`
}

func TestGCPResourceTagSerializationCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  GCPResourceTag
	}{
		{name: "When a tag is empty, it should round-trip across versions"},
		{name: "When a tag has a key and value, it should round-trip across versions", tag: GCPResourceTag{Key: "Environment", Value: "production"}},
		{name: "When a tag has the current project as parentID, old clients should retain key and value", tag: GCPResourceTag{ParentID: "customer-project", Key: "Environment", Value: "production"}},
		{name: "When a tag has an organization parentID, old clients should retain key and value", tag: GCPResourceTag{ParentID: "123456789012", Key: "Environment", Value: "production"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentJSON, err := json.Marshal(tc.tag)
			if err != nil {
				t.Fatal(err)
			}
			var old gcpResourceTagNMinus1
			if err := json.Unmarshal(currentJSON, &old); err != nil {
				t.Fatal(err)
			}
			if old.Key != tc.tag.Key || old.Value != tc.tag.Value {
				t.Fatalf("old client read %+v, want %+v", old, tc.tag)
			}
			oldJSON, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			var current GCPResourceTag
			if err := json.Unmarshal(oldJSON, &current); err != nil {
				t.Fatal(err)
			}
			wantFromOld := GCPResourceTag{Key: tc.tag.Key, Value: tc.tag.Value}
			if !reflect.DeepEqual(current, wantFromOld) {
				t.Fatalf("current client read %+v from old data, want %+v", current, wantFromOld)
			}
			var currentRoundTrip GCPResourceTag
			if err := json.Unmarshal(currentJSON, &currentRoundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(currentRoundTrip, tc.tag) {
				t.Fatalf("current client read %+v, want %+v", currentRoundTrip, tc.tag)
			}
		})
	}
}
