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

type dataPlaneManagedIdentitiesNMinus1 map[string]string

func TestAzureRegistryIdentitySerializationCompatibility(t *testing.T) {
	workloadIdentity := WorkloadIdentity{ClientID: "00000000-0000-0000-0000-000000000001"}
	managedIdentity := ManagedIdentity{
		ClientID:              "00000000-0000-0000-0000-000000000002",
		ObjectEncoding:        "utf-8",
		CredentialsSecretName: "image-registry",
	}

	t.Run("When current workload identity is set it should deserialize into the previous API", func(t *testing.T) {
		current := AzureWorkloadIdentities{ImageRegistry: workloadIdentity}
		data, err := json.Marshal(current)
		if err != nil {
			t.Fatalf("failed to marshal current workload identities: %v", err)
		}

		var previous azureWorkloadIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current workload identities: %v", err)
		}
		if !reflect.DeepEqual(previous.ImageRegistry, workloadIdentity) {
			t.Errorf("unexpected workload identity: got %#v, want %#v", previous.ImageRegistry, workloadIdentity)
		}
	})

	t.Run("When current workload identity is empty it should be omitted for the previous API", func(t *testing.T) {
		data, err := json.Marshal(AzureWorkloadIdentities{})
		if err != nil {
			t.Fatalf("failed to marshal current workload identities: %v", err)
		}
		if jsonContainsKey(t, data, "imageRegistry") {
			t.Errorf("imageRegistry must be omitted when unset: %s", data)
		}

		var previous azureWorkloadIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current workload identities: %v", err)
		}
		if !reflect.DeepEqual(previous.ImageRegistry, WorkloadIdentity{}) {
			t.Errorf("unexpected previous workload identity: got %#v", previous.ImageRegistry)
		}
	})

	t.Run("When previous workload identity is set it should deserialize into the current API", func(t *testing.T) {
		data, err := json.Marshal(azureWorkloadIdentitiesNMinus1{ImageRegistry: workloadIdentity})
		if err != nil {
			t.Fatalf("failed to marshal previous workload identities: %v", err)
		}

		var current AzureWorkloadIdentities
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatalf("current API failed to unmarshal previous workload identities: %v", err)
		}
		if !reflect.DeepEqual(current.ImageRegistry, workloadIdentity) {
			t.Errorf("unexpected current workload identity: got %#v, want %#v", current.ImageRegistry, workloadIdentity)
		}
	})

	t.Run("When current managed identity is set it should deserialize into the previous API", func(t *testing.T) {
		current := ControlPlaneManagedIdentities{ImageRegistry: managedIdentity}
		data, err := json.Marshal(current)
		if err != nil {
			t.Fatalf("failed to marshal current managed identities: %v", err)
		}

		var previous controlPlaneManagedIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current managed identities: %v", err)
		}
		if !reflect.DeepEqual(previous.ImageRegistry, managedIdentity) {
			t.Errorf("unexpected managed identity: got %#v, want %#v", previous.ImageRegistry, managedIdentity)
		}
	})

	t.Run("When current managed identity is empty it should be omitted for the previous API", func(t *testing.T) {
		data, err := json.Marshal(ControlPlaneManagedIdentities{})
		if err != nil {
			t.Fatalf("failed to marshal current managed identities: %v", err)
		}
		if jsonContainsKey(t, data, "imageRegistry") {
			t.Errorf("imageRegistry must be omitted when unset: %s", data)
		}

		var previous controlPlaneManagedIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current managed identities: %v", err)
		}
		if !reflect.DeepEqual(previous.ImageRegistry, ManagedIdentity{}) {
			t.Errorf("unexpected previous managed identity: got %#v", previous.ImageRegistry)
		}
	})

	t.Run("When previous managed identity is set it should deserialize into the current API", func(t *testing.T) {
		data, err := json.Marshal(controlPlaneManagedIdentitiesNMinus1{ImageRegistry: managedIdentity})
		if err != nil {
			t.Fatalf("failed to marshal previous managed identities: %v", err)
		}

		var current ControlPlaneManagedIdentities
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatalf("current API failed to unmarshal previous managed identities: %v", err)
		}
		if !reflect.DeepEqual(current.ImageRegistry, managedIdentity) {
			t.Errorf("unexpected current managed identity: got %#v, want %#v", current.ImageRegistry, managedIdentity)
		}
	})

	t.Run("When current data-plane identity is set it should deserialize into the previous API", func(t *testing.T) {
		const clientID = "00000000-0000-0000-0000-000000000003"
		data, err := json.Marshal(DataPlaneManagedIdentities{ImageRegistryMSIClientID: clientID})
		if err != nil {
			t.Fatalf("failed to marshal current data-plane identities: %v", err)
		}

		var previous dataPlaneManagedIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current data-plane identities: %v", err)
		}
		if previous["imageRegistryMSIClientID"] != clientID {
			t.Errorf("unexpected data-plane identity: got %q, want %q", previous["imageRegistryMSIClientID"], clientID)
		}
	})

	t.Run("When current data-plane identity is empty it should be omitted for the previous API", func(t *testing.T) {
		data, err := json.Marshal(DataPlaneManagedIdentities{})
		if err != nil {
			t.Fatalf("failed to marshal current data-plane identities: %v", err)
		}
		if jsonContainsKey(t, data, "imageRegistryMSIClientID") {
			t.Errorf("imageRegistryMSIClientID must be omitted when unset: %s", data)
		}

		var previous dataPlaneManagedIdentitiesNMinus1
		if err := json.Unmarshal(data, &previous); err != nil {
			t.Fatalf("previous API failed to unmarshal current data-plane identities: %v", err)
		}
		if previous["imageRegistryMSIClientID"] != "" {
			t.Errorf("unexpected previous data-plane identity: got %q", previous["imageRegistryMSIClientID"])
		}
	})

	t.Run("When previous data-plane identity is set it should deserialize into the current API", func(t *testing.T) {
		const clientID = "00000000-0000-0000-0000-000000000004"
		data, err := json.Marshal(dataPlaneManagedIdentitiesNMinus1{"imageRegistryMSIClientID": clientID})
		if err != nil {
			t.Fatalf("failed to marshal previous data-plane identities: %v", err)
		}

		var current DataPlaneManagedIdentities
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatalf("current API failed to unmarshal previous data-plane identities: %v", err)
		}
		if current.ImageRegistryMSIClientID != clientID {
			t.Errorf("unexpected current data-plane identity: got %q, want %q", current.ImageRegistryMSIClientID, clientID)
		}
	})

	t.Run("When previous data-plane identity is empty it should deserialize into the current API", func(t *testing.T) {
		data, err := json.Marshal(dataPlaneManagedIdentitiesNMinus1{"imageRegistryMSIClientID": ""})
		if err != nil {
			t.Fatalf("failed to marshal previous data-plane identities: %v", err)
		}
		if !jsonContainsKey(t, data, "imageRegistryMSIClientID") {
			t.Errorf("previous API should serialize its required imageRegistryMSIClientID: %s", data)
		}

		var current DataPlaneManagedIdentities
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatalf("current API failed to unmarshal previous data-plane identities: %v", err)
		}
		if current.ImageRegistryMSIClientID != "" {
			t.Errorf("unexpected current data-plane identity: got %q", current.ImageRegistryMSIClientID)
		}
	})
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
