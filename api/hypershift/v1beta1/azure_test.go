package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
)

type azureWorkloadIdentitiesNMinus1 struct {
	ImageRegistry WorkloadIdentity `json:"imageRegistry"` //nolint:kubeapilinter // test-only N-1 compatibility type
}

type controlPlaneManagedIdentitiesNMinus1 struct {
	ImageRegistry ManagedIdentity `json:"imageRegistry"` //nolint:kubeapilinter // test-only N-1 compatibility type
}

// dataPlaneManagedIdentitiesNMinus1 mirrors the previously released DataPlaneManagedIdentities
// with its exact JSON tags (no omitempty). Unlike a map, which preserves an omitted key, this
// struct reserializes an omitted/empty registry ID explicitly as "", matching how an N-1 typed
// Go client would read and write the field.
type dataPlaneManagedIdentitiesNMinus1 struct {
	ImageRegistryMSIClientID string `json:"imageRegistryMSIClientID"` //nolint:kubeapilinter // test-only N-1 compatibility type
	DiskMSIClientID          string `json:"diskMSIClientID"`          //nolint:kubeapilinter // test-only N-1 compatibility type
	FileMSIClientID          string `json:"fileMSIClientID"`          //nolint:kubeapilinter // test-only N-1 compatibility type
}

// TestAzureWorkloadIdentitiesSerializationCompatibility verifies N-1/N+1 JSON compatibility for the
// workload-identity image registry field, including that the optional field is omitted when unset.
func TestAzureWorkloadIdentitiesSerializationCompatibility(t *testing.T) {
	t.Parallel()
	workloadIdentity := WorkloadIdentity{ClientID: "00000000-0000-0000-0000-000000000001"}

	tests := []struct {
		name string
		// Exactly one direction is set per case.
		current  *AzureWorkloadIdentities        // current -> previous
		previous *azureWorkloadIdentitiesNMinus1 // previous -> current
		checkKey bool                            // assert presence of the "imageRegistry" key on the current marshal
		wantKey  bool                            // expected presence when checkKey is true
		want     WorkloadIdentity                // expected decoded ImageRegistry on the receiving side
	}{
		{
			name:    "When current workload identity is set it should deserialize into the previous API",
			current: &AzureWorkloadIdentities{ImageRegistry: workloadIdentity},
			want:    workloadIdentity,
		},
		{
			name:     "When current workload identity is empty it should be omitted for the previous API",
			current:  &AzureWorkloadIdentities{},
			checkKey: true,
			wantKey:  false,
			want:     WorkloadIdentity{},
		},
		{
			name:     "When previous workload identity is set it should deserialize into the current API",
			previous: &azureWorkloadIdentitiesNMinus1{ImageRegistry: workloadIdentity},
			want:     workloadIdentity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.current != nil {
				data, err := json.Marshal(*tc.current)
				if err != nil {
					t.Fatalf("failed to marshal current workload identities: %v", err)
				}
				if tc.checkKey {
					if got := jsonContainsKey(t, data, "imageRegistry"); got != tc.wantKey {
						t.Errorf("imageRegistry key presence = %v, want %v: %s", got, tc.wantKey, data)
					}
				}
				var previous azureWorkloadIdentitiesNMinus1
				if err := json.Unmarshal(data, &previous); err != nil {
					t.Fatalf("previous API failed to unmarshal current workload identities: %v", err)
				}
				if !reflect.DeepEqual(previous.ImageRegistry, tc.want) {
					t.Errorf("unexpected previous workload identity: got %#v, want %#v", previous.ImageRegistry, tc.want)
				}
				return
			}

			data, err := json.Marshal(*tc.previous)
			if err != nil {
				t.Fatalf("failed to marshal previous workload identities: %v", err)
			}
			var current AzureWorkloadIdentities
			if err := json.Unmarshal(data, &current); err != nil {
				t.Fatalf("current API failed to unmarshal previous workload identities: %v", err)
			}
			if !reflect.DeepEqual(current.ImageRegistry, tc.want) {
				t.Errorf("unexpected current workload identity: got %#v, want %#v", current.ImageRegistry, tc.want)
			}
		})
	}
}

// TestControlPlaneManagedIdentitiesSerializationCompatibility verifies N-1/N+1 JSON compatibility for
// the control-plane managed image registry identity, including that the optional field is omitted
// when unset.
func TestControlPlaneManagedIdentitiesSerializationCompatibility(t *testing.T) {
	t.Parallel()
	managedIdentity := ManagedIdentity{
		ClientID:              "00000000-0000-0000-0000-000000000002",
		ObjectEncoding:        "utf-8",
		CredentialsSecretName: "image-registry",
	}

	tests := []struct {
		name     string
		current  *ControlPlaneManagedIdentities        // current -> previous
		previous *controlPlaneManagedIdentitiesNMinus1 // previous -> current
		checkKey bool                                  // assert presence of the "imageRegistry" key on the current marshal
		wantKey  bool                                  // expected presence when checkKey is true
		want     ManagedIdentity                       // expected decoded ImageRegistry on the receiving side
	}{
		{
			name:    "When current managed identity is set it should deserialize into the previous API",
			current: &ControlPlaneManagedIdentities{ImageRegistry: managedIdentity},
			want:    managedIdentity,
		},
		{
			name:     "When current managed identity is empty it should be omitted for the previous API",
			current:  &ControlPlaneManagedIdentities{},
			checkKey: true,
			wantKey:  false,
			want:     ManagedIdentity{},
		},
		{
			name:     "When previous managed identity is set it should deserialize into the current API",
			previous: &controlPlaneManagedIdentitiesNMinus1{ImageRegistry: managedIdentity},
			want:     managedIdentity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.current != nil {
				data, err := json.Marshal(*tc.current)
				if err != nil {
					t.Fatalf("failed to marshal current managed identities: %v", err)
				}
				if tc.checkKey {
					if got := jsonContainsKey(t, data, "imageRegistry"); got != tc.wantKey {
						t.Errorf("imageRegistry key presence = %v, want %v: %s", got, tc.wantKey, data)
					}
				}
				var previous controlPlaneManagedIdentitiesNMinus1
				if err := json.Unmarshal(data, &previous); err != nil {
					t.Fatalf("previous API failed to unmarshal current managed identities: %v", err)
				}
				if !reflect.DeepEqual(previous.ImageRegistry, tc.want) {
					t.Errorf("unexpected previous managed identity: got %#v, want %#v", previous.ImageRegistry, tc.want)
				}
				return
			}

			data, err := json.Marshal(*tc.previous)
			if err != nil {
				t.Fatalf("failed to marshal previous managed identities: %v", err)
			}
			var current ControlPlaneManagedIdentities
			if err := json.Unmarshal(data, &current); err != nil {
				t.Fatalf("current API failed to unmarshal previous managed identities: %v", err)
			}
			if !reflect.DeepEqual(current.ImageRegistry, tc.want) {
				t.Errorf("unexpected current managed identity: got %#v, want %#v", current.ImageRegistry, tc.want)
			}
		})
	}
}

// TestDataPlaneManagedIdentitiesSerializationCompatibility verifies N-1/N+1 JSON compatibility for the
// data-plane image registry client ID. Unlike the workload and control-plane identities, this field
// has no omitempty, so an empty value must remain explicitly present on the wire in both directions.
func TestDataPlaneManagedIdentitiesSerializationCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// Exactly one of current/previous is set; roundTrip re-marshals the current decode of previous.
		current   *DataPlaneManagedIdentities        // current -> previous
		previous  *dataPlaneManagedIdentitiesNMinus1 // previous -> current (or round-trip when roundTrip is true)
		roundTrip bool
		checkKey  bool   // assert presence of the "imageRegistryMSIClientID" key on the produced JSON
		wantKey   bool   // expected presence when checkKey is true
		want      string // expected decoded ImageRegistryMSIClientID on the receiving side
	}{
		{
			name:    "When current data-plane identity is set it should deserialize into the previous API",
			current: &DataPlaneManagedIdentities{ImageRegistryMSIClientID: "00000000-0000-0000-0000-000000000003"},
			want:    "00000000-0000-0000-0000-000000000003",
		},
		{
			name:     "When current data-plane identity is empty, it should remain explicit for the previous API",
			current:  &DataPlaneManagedIdentities{},
			checkKey: true,
			wantKey:  true,
			want:     "",
		},
		{
			name:     "When previous data-plane identity is set it should deserialize into the current API",
			previous: &dataPlaneManagedIdentitiesNMinus1{ImageRegistryMSIClientID: "00000000-0000-0000-0000-000000000004"},
			want:     "00000000-0000-0000-0000-000000000004",
		},
		{
			name:     "When previous data-plane identity is empty it should deserialize into the current API",
			previous: &dataPlaneManagedIdentitiesNMinus1{},
			checkKey: true,
			wantKey:  true,
			want:     "",
		},
		{
			name:      "When previous data-plane identity is empty and re-serialized by the current API, it should remain explicit",
			previous:  &dataPlaneManagedIdentitiesNMinus1{},
			roundTrip: true,
			checkKey:  true,
			wantKey:   true,
			want:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.current != nil {
				data, err := json.Marshal(*tc.current)
				if err != nil {
					t.Fatalf("failed to marshal current data-plane identities: %v", err)
				}
				if tc.checkKey {
					if got := jsonContainsKey(t, data, "imageRegistryMSIClientID"); got != tc.wantKey {
						t.Errorf("imageRegistryMSIClientID key presence = %v, want %v: %s", got, tc.wantKey, data)
					}
				}
				var previous dataPlaneManagedIdentitiesNMinus1
				if err := json.Unmarshal(data, &previous); err != nil {
					t.Fatalf("previous API failed to unmarshal current data-plane identities: %v", err)
				}
				if previous.ImageRegistryMSIClientID != tc.want {
					t.Errorf("unexpected previous data-plane identity: got %q, want %q", previous.ImageRegistryMSIClientID, tc.want)
				}
				return
			}

			data, err := json.Marshal(*tc.previous)
			if err != nil {
				t.Fatalf("failed to marshal previous data-plane identities: %v", err)
			}
			// The N-1 marshal always carries the required key; assert it for the non-round-trip cases.
			if tc.checkKey && !tc.roundTrip {
				if got := jsonContainsKey(t, data, "imageRegistryMSIClientID"); got != tc.wantKey {
					t.Errorf("previous imageRegistryMSIClientID key presence = %v, want %v: %s", got, tc.wantKey, data)
				}
			}
			var current DataPlaneManagedIdentities
			if err := json.Unmarshal(data, &current); err != nil {
				t.Fatalf("current API failed to unmarshal previous data-plane identities: %v", err)
			}
			if current.ImageRegistryMSIClientID != tc.want {
				t.Errorf("unexpected current data-plane identity: got %q, want %q", current.ImageRegistryMSIClientID, tc.want)
			}
			if !tc.roundTrip {
				return
			}
			// Re-serialize with the current API and confirm the empty value is still explicit.
			currentData, err := json.Marshal(current)
			if err != nil {
				t.Fatalf("failed to re-marshal current data-plane identities: %v", err)
			}
			if tc.checkKey {
				if got := jsonContainsKey(t, currentData, "imageRegistryMSIClientID"); got != tc.wantKey {
					t.Errorf("re-serialized imageRegistryMSIClientID key presence = %v, want %v: %s", got, tc.wantKey, currentData)
				}
			}
		})
	}
}

func jsonContainsKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("failed to unmarshal JSON object: %v", err)
	}
	_, exists := object[key]
	return exists
}
