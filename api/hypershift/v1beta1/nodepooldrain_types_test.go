package v1beta1

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestHostedControlPlaneDrainStatusSerializationCompatibility(t *testing.T) {
	// These are the N-1 JSON shapes, before the probe timestamp and string drain state.
	type nodePoolDrainNMinus1 struct {
		NodePoolName  *string `json:"nodePoolName,omitempty"`
		Blocked       *bool   `json:"blocked,omitempty"`
		BlockingCount *int32  `json:"blockingCount,omitempty"`
		Reason        *string `json:"reason,omitempty"`
	}
	type hostedControlPlaneDrainStatusNMinus1 struct {
		NodePoolDrain []nodePoolDrainNMinus1 `json:"nodePoolDrain,omitempty"`
	}

	probeTime := metav1.NewTime(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	entry := NodePoolDrain{NodePoolName: "pool-a", State: NodePoolDrainStateBlocked}
	for _, tc := range []struct {
		name      string
		current   HostedControlPlaneStatus
		wantProbe bool
	}{
		{name: "probe present", current: HostedControlPlaneStatus{
			NodePoolDrain: []NodePoolDrain{entry}, NodePoolDrainLastProbeTime: &probeTime,
		}, wantProbe: true},
		{name: "probe absent", current: HostedControlPlaneStatus{
			NodePoolDrain: []NodePoolDrain{entry},
		}},
		{name: "zero value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentJSON, err := json.Marshal(tc.current)
			if err != nil {
				t.Fatal(err)
			}
			var currentFields map[string]json.RawMessage
			if err := json.Unmarshal(currentJSON, &currentFields); err != nil {
				t.Fatal(err)
			}
			if _, present := currentFields["nodePoolDrainLastProbeTime"]; present != tc.wantProbe {
				t.Fatalf("probe field present=%t, want %t: %s", present, tc.wantProbe, currentJSON)
			}

			// N-1 readers ignore the new field and preserve existing drain data.
			var old hostedControlPlaneDrainStatusNMinus1
			if err := json.Unmarshal(currentJSON, &old); err != nil {
				t.Fatalf("N-1 cannot read current status: %v", err)
			}
			if len(old.NodePoolDrain) != len(tc.current.NodePoolDrain) {
				t.Fatalf("N-1 lost drain entries: got %d, want %d", len(old.NodePoolDrain), len(tc.current.NodePoolDrain))
			}

			// N+1 readers accept old data without a timestamp, then fail open.
			oldJSON, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			var roundTripped HostedControlPlaneStatus
			if err := json.Unmarshal(oldJSON, &roundTripped); err != nil {
				t.Fatalf("current version cannot read N-1 status: %v", err)
			}
			if roundTripped.NodePoolDrainLastProbeTime != nil {
				t.Fatalf("N-1 status unexpectedly acquired a probe time: %v", roundTripped.NodePoolDrainLastProbeTime)
			}
			if len(roundTripped.NodePoolDrain) != len(tc.current.NodePoolDrain) {
				t.Fatalf("N+1 lost drain entries: got %d, want %d", len(roundTripped.NodePoolDrain), len(tc.current.NodePoolDrain))
			}
			if len(roundTripped.NodePoolDrain) > 0 && roundTripped.NodePoolDrain[0].State != "" {
				t.Fatalf("N-1 status unexpectedly acquired a drain state: %+v", roundTripped.NodePoolDrain[0])
			}
		})
	}

	t.Run("legacy blocked boolean", func(t *testing.T) {
		old := hostedControlPlaneDrainStatusNMinus1{NodePoolDrain: []nodePoolDrainNMinus1{{
			NodePoolName: ptr.To("pool-a"), Blocked: ptr.To(true),
		}}}
		oldJSON, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		var current HostedControlPlaneStatus
		if err := json.Unmarshal(oldJSON, &current); err != nil {
			t.Fatalf("current version cannot read N-1 boolean status: %v", err)
		}
		if len(current.NodePoolDrain) != 1 || current.NodePoolDrain[0].State != "" {
			t.Fatalf("legacy boolean must have an unverified state: %+v", current.NodePoolDrain)
		}
	})
}
