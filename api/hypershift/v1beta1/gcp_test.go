package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Validation changed, but the GCPResourceTag wire representation must remain
// readable by clients built against the earlier API-only version of the type.
type gcpResourceTagNMinus1 struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

func TestGCPResourceTagSerializationCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		tag  GCPResourceTag
	}{
		{name: "When a tag is empty, it should round-trip across versions"},
		{name: "When a tag has a key and value, it should round-trip across versions", tag: GCPResourceTag{Key: "Environment", Value: "production"}},
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
			if !reflect.DeepEqual(current, tc.tag) {
				t.Fatalf("current client read %+v, want %+v", current, tc.tag)
			}
		})
	}
}
