/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestEffectsSerializationCompatibility(t *testing.T) {
	// Keep the historical wire schema local: this fixture is not a new API type.
	type effectsNMinus1 struct {
		// kasGoMemLimit is the legacy KAS memory limit.
		// +optional
		KASGoMemLimit *string `json:"kasGoMemLimit,omitempty"`
		// controlPlanePriorityClassName is the legacy default priority class.
		// +optional
		ControlPlanePriorityClassName *string `json:"controlPlanePriorityClassName,omitempty"`
		// etcdPriorityClassName is the legacy etcd priority class.
		// +optional
		EtcdPriorityClassName *string `json:"etcdPriorityClassName,omitempty"`
		// APICriticalPriorityClassName is the legacy API priority class.
		// +optional
		APICriticalPriorityClassName *string `json:"APICriticalPriorityClassName,omitempty"`
		// resourceRequests contains the legacy per-container requests.
		// +optional
		ResourceRequests []ResourceRequest `json:"resourceRequests,omitempty"`
		// machineHealthCheckTimeout is the legacy health check timeout.
		// +optional
		MachineHealthCheckTimeout *metav1.Duration `json:"machineHealthCheckTimeout,omitempty"`
		// maximumRequestsInflight is the legacy KAS inflight limit.
		// +optional
		MaximumRequestsInflight *int `json:"maximumRequestsInflight,omitempty"`
		// maximumMutatingRequestsInflight is the legacy KAS mutating inflight limit.
		// +optional
		MaximumMutatingRequestsInflight *int `json:"maximumMutatingRequestsInflight,omitempty"`
	}

	for _, tt := range []struct {
		name   string
		policy ContainerResourcePolicy
		legacy effectsNMinus1
	}{
		{name: "When effects are empty, it should omit the new policy and preserve nil pointers"},
		{
			name:   "When legacy effects are configured, it should preserve them without enabling the policy",
			legacy: effectsNMinus1{KASGoMemLimit: ptr.To("1GiB"), ResourceRequests: []ResourceRequest{{DeploymentName: "etcd", ContainerName: "etcd", CPU: ptr.To(resource.MustParse("100m"))}}},
		},
		{
			name:   "When the new policy is configured, it should remain readable by old clients",
			legacy: effectsNMinus1{KASGoMemLimit: ptr.To("1GiB")},
			policy: ContainerResourcePolicy{
				DefaultRequests:      ContainerRequests{CPU: resource.MustParse("100m"), Memory: resource.MustParse("128Mi")},
				GoMemoryLimitPercent: 80, MemoryLimitMultiplier: 2,
				Containers: []ContainerResources{{
					Workload: "etcd", Container: "etcd",
					Requests:             ContainerRequests{CPU: resource.MustParse("1"), Memory: resource.MustParse("1Gi")},
					GoMemoryLimitPercent: ptr.To[int32](0), GoMaxProcs: 2,
				}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			oldJSON, err := json.Marshal(tt.legacy)
			if err != nil {
				t.Fatal(err)
			}
			var current Effects
			if err := json.Unmarshal(oldJSON, &current); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(current.ContainerResourcePolicy, ContainerResourcePolicy{}) {
				t.Fatal("old data unexpectedly enabled container resource policy")
			}
			current.ContainerResourcePolicy = tt.policy
			newJSON, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			var old effectsNMinus1
			if err := json.Unmarshal(newJSON, &old); err != nil {
				t.Fatal(err)
			}
			roundTripJSON, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			if string(oldJSON) != string(roundTripJSON) {
				t.Fatalf("legacy fields changed: %s != %s", oldJSON, roundTripJSON)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(newJSON, &wire); err != nil {
				t.Fatal(err)
			}
			if _, present := wire["containerResourcePolicy"]; present == tt.policy.DefaultRequests.CPU.IsZero() {
				t.Fatalf("unexpected policy presence in %s", newJSON)
			}
			var decoded Effects
			if err := json.Unmarshal(newJSON, &decoded); err != nil {
				t.Fatal(err)
			}
			decodedJSON, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(newJSON) != string(decodedJSON) {
				t.Fatalf("policy did not round-trip: %s != %s", newJSON, decodedJSON)
			}
		})
	}
}

func TestContainerResourcesSerialization(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input ContainerResources
		want  string
	}{
		{name: "When all fields are zero, it should omit structs and nil pointers", want: `{}`},
		{name: "When memory management is explicitly disabled, it should retain zero", input: ContainerResources{GoMemoryLimitPercent: ptr.To[int32](0)}, want: `{"goMemoryLimitPercent":0}`},
		{name: "When only CPU is set, it should serialize zero memory for admission to reject", input: ContainerResources{Requests: ContainerRequests{CPU: resource.MustParse("100m")}}, want: `{"requests":{"cpu":"100m","memory":"0"}}`},
		{name: "When GoMaxProcs is zero, it should omit the override", input: ContainerResources{GoMaxProcs: 0}, want: `{}`},
		{name: "When GoMaxProcs is positive, it should serialize the override", input: ContainerResources{GoMaxProcs: 2}, want: `{"goMaxProcs":2}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Fatalf("got %s, want %s", data, tt.want)
			}
		})
	}
}

func TestContainerResourcePolicySerialization(t *testing.T) {
	for _, tt := range []struct {
		name string
		wire string
	}{
		{"When limits are omitted, it should omit zero values", `{}`},
		{"When a multiplier is read, it should preserve its wire format", `{"memoryLimitMultiplier":3}`},
		{"When CPU limits are explicitly disabled, it should retain None", `{"cpuLimitPolicy":"None"}`},
		{"When constrained limits are enabled, it should retain both opt-ins", `{"memoryLimitPercent":110,"cpuLimitPolicy":"EqualsRequest"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var policy ContainerResourcePolicy
			if err := json.Unmarshal([]byte(tt.wire), &policy); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(policy)
			if err != nil || string(encoded) != tt.wire {
				t.Fatalf("wire changed: %s, %v", encoded, err)
			}
		})
	}
}
