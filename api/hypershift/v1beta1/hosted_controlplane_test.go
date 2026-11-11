package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
)

// hostedControlPlaneStatusNMinus1 is a shape probe for the N-1 version of
// HostedControlPlaneStatus, before the operatorConfiguration field was added. Consumers
// such as ARO-HCP embed these types and serialize them to their own storage outside CRD
// validation, so a rollback has to be able to read data written by the newer type.
type hostedControlPlaneStatusNMinus1 struct {
	// ready reports whether the control plane is ready.
	Ready bool `json:"ready"` //nolint:kubeapilinter // test-only N-1 compat struct
	// initialized reports whether the control plane is initialized.
	Initialized bool `json:"initialized"` //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestOperatorConfigurationStatusSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// current is the N (current) version of the struct.
		current OperatorConfigurationStatus
		// expectedJSON is the expected JSON output from marshaling current.
		expectedJSON string
	}{
		{
			name:         "When nothing has been reported it should serialize to an empty object",
			current:      OperatorConfigurationStatus{},
			expectedJSON: `{}`,
		},
		{
			name: "When a generation is observed with no settings configured it should omit honoredFields",
			current: OperatorConfigurationStatus{
				ObservedGeneration: 4,
			},
			expectedJSON: `{"observedGeneration":4}`,
		},
		{
			name: "When settings are honored it should serialize both fields",
			current: OperatorConfigurationStatus{
				ObservedGeneration: 7,
				HonoredFields:      []string{"etcd.logLevel", "kubeAPIServer.eventTTLMinutes"},
			},
			expectedJSON: `{"observedGeneration":7,"honoredFields":["etcd.logLevel","kubeAPIServer.eventTTLMinutes"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current type: %v", err)
			}
			if string(encoded) != tt.expectedJSON {
				t.Errorf("unexpected JSON:\n got: %s\nwant: %s", encoded, tt.expectedJSON)
			}

			// N (forward): the current type reads back what it wrote.
			var roundTripped OperatorConfigurationStatus
			if err := json.Unmarshal(encoded, &roundTripped); err != nil {
				t.Fatalf("failed to unmarshal into current type: %v", err)
			}
			if !reflect.DeepEqual(roundTripped, tt.current) {
				t.Errorf("round trip changed the value:\n got: %#v\nwant: %#v", roundTripped, tt.current)
			}
		})
	}
}

func TestHostedControlPlaneStatusSerializationCompatibility(t *testing.T) {
	// N-1 (rollback): a consumer without the operatorConfiguration field must still be
	// able to deserialize a status written by the current type.
	current := HostedControlPlaneStatus{
		Ready:       true,
		Initialized: true,
		OperatorConfiguration: OperatorConfigurationStatus{
			ObservedGeneration: 7,
			HonoredFields:      []string{"kubeAPIServer.eventTTLMinutes"},
		},
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("failed to marshal current type: %v", err)
	}

	var nMinus1 hostedControlPlaneStatusNMinus1
	if err := json.Unmarshal(encoded, &nMinus1); err != nil {
		t.Fatalf("N-1 consumer failed to deserialize status written by the current type: %v", err)
	}
	if want := (hostedControlPlaneStatusNMinus1{Ready: true, Initialized: true}); nMinus1 != want {
		t.Errorf("N-1 consumer read unexpected values:\n got: %#v\nwant: %#v", nMinus1, want)
	}

	// N+1 (forward): the current type must read a status written before the field
	// existed, and see the zero value rather than failing.
	olderEncoded, err := json.Marshal(hostedControlPlaneStatusNMinus1{Ready: true, Initialized: true})
	if err != nil {
		t.Fatalf("failed to marshal N-1 type: %v", err)
	}
	var fromOlder HostedControlPlaneStatus
	if err := json.Unmarshal(olderEncoded, &fromOlder); err != nil {
		t.Fatalf("current type failed to deserialize status written by an N-1 consumer: %v", err)
	}
	if !reflect.DeepEqual(fromOlder.OperatorConfiguration, OperatorConfigurationStatus{}) {
		t.Errorf("expected the zero OperatorConfigurationStatus from an N-1 status, got: %#v", fromOlder.OperatorConfiguration)
	}
}
