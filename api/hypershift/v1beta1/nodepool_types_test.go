package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/utils/ptr"
)

// These types represent the N-1 (previous) version of the API structs,
// before the int32 -> *int32 pointer change. They are used to verify
// that JSON produced by the current types can be deserialized by
// previous versions of the code, and vice versa.
type nodePoolAutoScalingNMinus1 struct {
	Min int32 `json:"min"`
	Max int32 `json:"max"`
}

type awsNodePoolPlatformNMinus1 struct {
	// instanceType is the EC2 instance type.
	InstanceType string `json:"instanceType"` //nolint:kubeapilinter // test-only N-1 compat struct
	// subnet is the subnet reference.
	Subnet AWSResourceReference `json:"subnet"` //nolint:kubeapilinter // test-only N-1 compat struct
}

// azureNodePoolPlatformNMinus1 is a shape probe for the N-1 version of AzureNodePoolPlatform,
// before the ipForwarding field was added. It carries only the required fields, which is enough
// to prove that an older consumer can deserialize JSON written by the current type.
type azureNodePoolPlatformNMinus1 struct {
	// vmSize is the Azure VM instance type.
	VMSize string `json:"vmSize"` //nolint:kubeapilinter // test-only N-1 compat struct
	// image is the VM image to boot.
	Image AzureVMImage `json:"image"` //nolint:kubeapilinter // test-only N-1 compat struct
	// osDisk is the OS disk configuration.
	OSDisk AzureNodePoolOSDisk `json:"osDisk"` //nolint:kubeapilinter // test-only N-1 compat struct
	// subnetID is the subnet the VMs are placed in.
	SubnetID string `json:"subnetID"` //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestNodePoolAutoScalingSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// current is the N (current) version of the struct
		current NodePoolAutoScaling
		// expectedJSON is the expected JSON output from marshalling current
		expectedJSON string
		// nMinus1Result is the expected result when unmarshalling into the N-1 struct
		nMinus1Result nodePoolAutoScalingNMinus1
	}{
		{
			name: "When Min is set to a positive value it should round-trip to N-1",
			current: NodePoolAutoScaling{
				Min: ptr.To[int32](3),
				Max: 5,
			},
			expectedJSON:  `{"min":3,"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 3, Max: 5},
		},
		{
			name: "When Min is explicitly zero it should round-trip to N-1",
			current: NodePoolAutoScaling{
				Min: ptr.To[int32](0),
				Max: 5,
			},
			expectedJSON:  `{"min":0,"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 0, Max: 5},
		},
		{
			name: "When Min is nil it should be omitted and N-1 should deserialize as zero value",
			current: NodePoolAutoScaling{
				Min: nil,
				Max: 5,
			},
			expectedJSON:  `{"max":5}`,
			nMinus1Result: nodePoolAutoScalingNMinus1{Min: 0, Max: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal current (N) version
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			// Deserialize into N-1 struct
			var nMinus1 nodePoolAutoScalingNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1 != tt.nMinus1Result {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			// Reverse: marshal N-1 and deserialize into current (N)
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped NodePoolAutoScaling
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Max != tt.nMinus1Result.Max {
				t.Errorf("Max mismatch after N-1 round-trip: got %d, want %d", roundTripped.Max, tt.nMinus1Result.Max)
			}
			if ptr.Deref(roundTripped.Min, -1) != tt.nMinus1Result.Min {
				t.Errorf("Min mismatch after N-1 round-trip: got %v, want %d", roundTripped.Min, tt.nMinus1Result.Min)
			}
		})
	}
}

// awsResourceTagNMinus1 represents the previous version of AWSResourceTag
// (Key + Value only, no OverridePolicy). This is the N-1 fixture for both
// the deprecated AWSResourceTag and the new per-API types.
type awsResourceTagNMinus1 struct {
	// key is the tag key.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Key string `json:"key,omitempty"`
	// value is the tag value.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Value string `json:"value,omitempty"`
}

func TestAWSClusterResourceTagSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AWSClusterResourceTag
		expectedJSON  string
		nMinus1Result awsResourceTagNMinus1
	}{
		{
			name:          "When all fields are zero-value it should round-trip as empty object",
			current:       AWSClusterResourceTag{},
			expectedJSON:  `{}`,
			nMinus1Result: awsResourceTagNMinus1{},
		},
		{
			name:          "When overridePolicy is omitted it should omit the field",
			current:       AWSClusterResourceTag{Key: "env", Value: "prod"},
			expectedJSON:  `{"key":"env","value":"prod"}`,
			nMinus1Result: awsResourceTagNMinus1{Key: "env", Value: "prod"},
		},
		{
			name:          "When overridePolicy is Allow it should include the field",
			current:       AWSClusterResourceTag{Key: "env", Value: "prod", OverridePolicy: AWSResourceTagOverridePolicyAllow},
			expectedJSON:  `{"key":"env","value":"prod","overridePolicy":"Allow"}`,
			nMinus1Result: awsResourceTagNMinus1{Key: "env", Value: "prod"},
		},
		{
			name:          "When overridePolicy is Deny it should include the field",
			current:       AWSClusterResourceTag{Key: "env", Value: "prod", OverridePolicy: AWSResourceTagOverridePolicyDeny},
			expectedJSON:  `{"key":"env","value":"prod","overridePolicy":"Deny"}`,
			nMinus1Result: awsResourceTagNMinus1{Key: "env", Value: "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 awsResourceTagNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if !reflect.DeepEqual(nMinus1, tt.nMinus1Result) {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSClusterResourceTag
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Key != tt.nMinus1Result.Key {
				t.Errorf("Key mismatch after N-1 round-trip: got %s, want %s", roundTripped.Key, tt.nMinus1Result.Key)
			}
			if roundTripped.Value != tt.nMinus1Result.Value {
				t.Errorf("Value mismatch after N-1 round-trip: got %s, want %s", roundTripped.Value, tt.nMinus1Result.Value)
			}
			if roundTripped.OverridePolicy != "" {
				t.Errorf("OverridePolicy should be empty after N-1 round-trip: got %v", roundTripped.OverridePolicy)
			}
		})
	}
}

func TestAWSNodePoolResourceTagSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AWSNodePoolResourceTag
		expectedJSON  string
		nMinus1Result awsResourceTagNMinus1
	}{
		{
			name:          "When all fields are zero-value it should round-trip as empty object",
			current:       AWSNodePoolResourceTag{},
			expectedJSON:  `{}`,
			nMinus1Result: awsResourceTagNMinus1{},
		},
		{
			name:          "When key and value are set it should serialize correctly",
			current:       AWSNodePoolResourceTag{Key: "env", Value: "prod"},
			expectedJSON:  `{"key":"env","value":"prod"}`,
			nMinus1Result: awsResourceTagNMinus1{Key: "env", Value: "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 awsResourceTagNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if !reflect.DeepEqual(nMinus1, tt.nMinus1Result) {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSNodePoolResourceTag
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Key != tt.nMinus1Result.Key {
				t.Errorf("Key mismatch: got %s, want %s", roundTripped.Key, tt.nMinus1Result.Key)
			}
			if roundTripped.Value != tt.nMinus1Result.Value {
				t.Errorf("Value mismatch: got %s, want %s", roundTripped.Value, tt.nMinus1Result.Value)
			}
		})
	}
}

func TestAWSEndpointServiceResourceTagSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       AWSEndpointServiceResourceTag
		expectedJSON  string
		nMinus1Result awsResourceTagNMinus1
	}{
		{
			name:          "When all fields are zero-value it should round-trip as empty object",
			current:       AWSEndpointServiceResourceTag{},
			expectedJSON:  `{}`,
			nMinus1Result: awsResourceTagNMinus1{},
		},
		{
			name:          "When key and value are set it should serialize correctly",
			current:       AWSEndpointServiceResourceTag{Key: "env", Value: "prod"},
			expectedJSON:  `{"key":"env","value":"prod"}`,
			nMinus1Result: awsResourceTagNMinus1{Key: "env", Value: "prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 awsResourceTagNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if !reflect.DeepEqual(nMinus1, tt.nMinus1Result) {
				t.Errorf("N-1 deserialization mismatch: got %+v, want %+v", nMinus1, tt.nMinus1Result)
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSEndpointServiceResourceTag
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.Key != tt.nMinus1Result.Key {
				t.Errorf("Key mismatch: got %s, want %s", roundTripped.Key, tt.nMinus1Result.Key)
			}
			if roundTripped.Value != tt.nMinus1Result.Value {
				t.Errorf("Value mismatch: got %s, want %s", roundTripped.Value, tt.nMinus1Result.Value)
			}
		})
	}
}

func TestAWSNodePoolPlatformSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// current is the N (current) version of the struct
		current AWSNodePoolPlatform
		// expectedJSON is the expected JSON output from marshalling current
		expectedJSON string
		// nMinus1Result is the expected result when unmarshalling into the N-1 struct
		nMinus1Result awsNodePoolPlatformNMinus1
	}{
		{
			name: "When cpuOptions are set it should round-trip to N-1",
			current: AWSNodePoolPlatform{
				InstanceType: "m6i.large",
				Subnet: AWSResourceReference{
					ID: ptr.To("subnet-1234567890abcdef0"),
				},
				CPUOptions: CPUOptions{
					NestedVirtualizationPolicy: NestedVirtualizationEnabled,
				},
			},
			expectedJSON: `{"instanceType":"m6i.large","subnet":{"id":"subnet-1234567890abcdef0"},"cpuOptions":{"nestedVirtualizationPolicy":"Enabled"}}`,
			nMinus1Result: awsNodePoolPlatformNMinus1{
				InstanceType: "m6i.large",
				Subnet: AWSResourceReference{
					ID: ptr.To("subnet-1234567890abcdef0"),
				},
			},
		},
		{
			name: "When cpuOptions are omitted it should preserve N-1 JSON shape",
			current: AWSNodePoolPlatform{
				InstanceType: "m6i.large",
				Subnet: AWSResourceReference{
					ID: ptr.To("subnet-1234567890abcdef0"),
				},
			},
			expectedJSON: `{"instanceType":"m6i.large","subnet":{"id":"subnet-1234567890abcdef0"}}`,
			nMinus1Result: awsNodePoolPlatformNMinus1{
				InstanceType: "m6i.large",
				Subnet: AWSResourceReference{
					ID: ptr.To("subnet-1234567890abcdef0"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 awsNodePoolPlatformNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1.InstanceType != tt.nMinus1Result.InstanceType {
				t.Errorf("N-1 instanceType mismatch: got %s, want %s", nMinus1.InstanceType, tt.nMinus1Result.InstanceType)
			}
			if ptr.Deref(nMinus1.Subnet.ID, "") != ptr.Deref(tt.nMinus1Result.Subnet.ID, "") {
				t.Errorf("N-1 subnet ID mismatch: got %q, want %q", ptr.Deref(nMinus1.Subnet.ID, ""), ptr.Deref(tt.nMinus1Result.Subnet.ID, ""))
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AWSNodePoolPlatform
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.InstanceType != tt.nMinus1Result.InstanceType {
				t.Errorf("InstanceType mismatch after N-1 round-trip: got %s, want %s", roundTripped.InstanceType, tt.nMinus1Result.InstanceType)
			}
			if ptr.Deref(roundTripped.Subnet.ID, "") != ptr.Deref(tt.nMinus1Result.Subnet.ID, "") {
				t.Errorf("Subnet ID mismatch after N-1 round-trip: got %q, want %q", ptr.Deref(roundTripped.Subnet.ID, ""), ptr.Deref(tt.nMinus1Result.Subnet.ID, ""))
			}
			if roundTripped.CPUOptions != (CPUOptions{}) {
				t.Errorf("CPUOptions mismatch after N-1 round-trip: got %+v, want zero value", roundTripped.CPUOptions)
			}
		})
	}
}

// TestAzureNodePoolPlatformSerializationCompatibility verifies that adding ipForwarding to
// AzureNodePoolPlatform is safe in both directions for consumers that vendor these types and
// serialize them outside CRD validation (e.g. ARO-HCP to Cosmos DB): current JSON still
// deserializes into an N-1 struct, N-1 JSON still deserializes into the current type, and an
// omitted ipForwarding never reaches the wire. That last property is what keeps the generated
// AzureMachineTemplate hash — and therefore the whole Azure fleet — stable on upgrade.
func TestAzureNodePoolPlatformSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// current is the N (current) version of the struct
		current AzureNodePoolPlatform
		// expectedJSON is the expected JSON output from marshalling current
		expectedJSON string
		// nMinus1Result is the expected result when unmarshalling into the N-1 struct
		nMinus1Result azureNodePoolPlatformNMinus1
	}{
		{
			name: "When ipForwarding is set it should round-trip to N-1",
			current: AzureNodePoolPlatform{
				VMSize: "Standard_D4s_v5",
				Image: AzureVMImage{
					Type:    ImageID,
					ImageID: ptr.To("test-image-id"),
				},
				OSDisk: AzureNodePoolOSDisk{
					SizeGiB:                120,
					DiskStorageAccountType: DiskStorageAccountTypesPremiumLRS,
				},
				SubnetID:     "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				IPForwarding: AzureIPForwardingEnabled,
			},
			expectedJSON: `{"vmSize":"Standard_D4s_v5","image":{"type":"ImageID","imageID":"test-image-id"},"osDisk":{"sizeGiB":120,"diskStorageAccountType":"Premium_LRS"},"subnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet","ipForwarding":"Enabled"}`,
			nMinus1Result: azureNodePoolPlatformNMinus1{
				VMSize: "Standard_D4s_v5",
				Image: AzureVMImage{
					Type:    ImageID,
					ImageID: ptr.To("test-image-id"),
				},
				OSDisk: AzureNodePoolOSDisk{
					SizeGiB:                120,
					DiskStorageAccountType: DiskStorageAccountTypesPremiumLRS,
				},
				SubnetID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			},
		},
		{
			name: "When ipForwarding is omitted it should preserve N-1 JSON shape",
			current: AzureNodePoolPlatform{
				VMSize: "Standard_D4s_v5",
				Image: AzureVMImage{
					Type:    ImageID,
					ImageID: ptr.To("test-image-id"),
				},
				OSDisk: AzureNodePoolOSDisk{
					SizeGiB:                120,
					DiskStorageAccountType: DiskStorageAccountTypesPremiumLRS,
				},
				SubnetID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			},
			expectedJSON: `{"vmSize":"Standard_D4s_v5","image":{"type":"ImageID","imageID":"test-image-id"},"osDisk":{"sizeGiB":120,"diskStorageAccountType":"Premium_LRS"},"subnetID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"}`,
			nMinus1Result: azureNodePoolPlatformNMinus1{
				VMSize: "Standard_D4s_v5",
				Image: AzureVMImage{
					Type:    ImageID,
					ImageID: ptr.To("test-image-id"),
				},
				OSDisk: AzureNodePoolOSDisk{
					SizeGiB:                120,
					DiskStorageAccountType: DiskStorageAccountTypesPremiumLRS,
				},
				SubnetID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 azureNodePoolPlatformNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if nMinus1.VMSize != tt.nMinus1Result.VMSize {
				t.Errorf("N-1 vmSize mismatch: got %s, want %s", nMinus1.VMSize, tt.nMinus1Result.VMSize)
			}
			if !reflect.DeepEqual(nMinus1.Image, tt.nMinus1Result.Image) {
				t.Errorf("N-1 image mismatch: got %+v, want %+v", nMinus1.Image, tt.nMinus1Result.Image)
			}
			if !reflect.DeepEqual(nMinus1.OSDisk, tt.nMinus1Result.OSDisk) {
				t.Errorf("N-1 osDisk mismatch: got %+v, want %+v", nMinus1.OSDisk, tt.nMinus1Result.OSDisk)
			}
			if nMinus1.SubnetID != tt.nMinus1Result.SubnetID {
				t.Errorf("N-1 subnetID mismatch: got %s, want %s", nMinus1.SubnetID, tt.nMinus1Result.SubnetID)
			}

			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped AzureNodePoolPlatform
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.VMSize != tt.nMinus1Result.VMSize {
				t.Errorf("VMSize mismatch after N-1 round-trip: got %s, want %s", roundTripped.VMSize, tt.nMinus1Result.VMSize)
			}
			if !reflect.DeepEqual(roundTripped.Image, tt.nMinus1Result.Image) {
				t.Errorf("Image mismatch after N-1 round-trip: got %+v, want %+v", roundTripped.Image, tt.nMinus1Result.Image)
			}
			if !reflect.DeepEqual(roundTripped.OSDisk, tt.nMinus1Result.OSDisk) {
				t.Errorf("OSDisk mismatch after N-1 round-trip: got %+v, want %+v", roundTripped.OSDisk, tt.nMinus1Result.OSDisk)
			}
			if roundTripped.SubnetID != tt.nMinus1Result.SubnetID {
				t.Errorf("SubnetID mismatch after N-1 round-trip: got %s, want %s", roundTripped.SubnetID, tt.nMinus1Result.SubnetID)
			}
			// Data written by an N-1 consumer carries no ipForwarding key, so the current type
			// must decode it as the zero value and leave IP forwarding off.
			if roundTripped.IPForwarding != "" {
				t.Errorf("IPForwarding mismatch after N-1 round-trip: got %q, want zero value", roundTripped.IPForwarding)
			}
		})
	}
}
