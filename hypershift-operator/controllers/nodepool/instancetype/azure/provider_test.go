package azure

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

type mockResourceSKUsAPI struct {
	skus []*armcompute.ResourceSKU
	err  error
}

func (m *mockResourceSKUsAPI) NewListPager(_ *armcompute.ResourceSKUsClientListOptions) *azruntime.Pager[armcompute.ResourceSKUsClientListResponse] {
	return azruntime.NewPager(azruntime.PagingHandler[armcompute.ResourceSKUsClientListResponse]{
		More: func(page armcompute.ResourceSKUsClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, page *armcompute.ResourceSKUsClientListResponse) (armcompute.ResourceSKUsClientListResponse, error) {
			if m.err != nil {
				return armcompute.ResourceSKUsClientListResponse{}, m.err
			}
			return armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: m.skus,
				},
			}, nil
		},
	})
}

func makeSKU(name, resourceType string, capabilities map[string]string) *armcompute.ResourceSKU {
	sku := &armcompute.ResourceSKU{
		Name:         to.Ptr(name),
		ResourceType: to.Ptr(resourceType),
	}
	for k, v := range capabilities {
		sku.Capabilities = append(sku.Capabilities, &armcompute.ResourceSKUCapabilities{
			Name:  to.Ptr(k),
			Value: to.Ptr(v),
		})
	}
	return sku
}

func TestTransformSKU_WhenValidInput_ItShouldTransformCorrectly(t *testing.T) {
	tests := []struct {
		name     string
		input    *armcompute.ResourceSKU
		expected *instancetype.InstanceTypeInfo
	}{
		{
			name: "When Standard_D4s_v3 with x64 arch it should transform correctly",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"MemoryGB":            "16",
				"CpuArchitectureType": "x64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_D4s_v3",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When GPU VM it should set GPU count",
			input: makeSKU("Standard_NC16as_T4_v3", "virtualMachines", map[string]string{
				"vCPUs":               "16",
				"MemoryGB":            "110",
				"GPUs":                "1",
				"CpuArchitectureType": "x64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_NC16as_T4_v3",
				VCPU:            16,
				MemoryMb:        112640,
				GPU:             1,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When Arm64 VM it should set correct architecture",
			input: makeSKU("Standard_D4ps_v5", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"MemoryGB":            "16",
				"CpuArchitectureType": "Arm64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_D4ps_v5",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureARM64,
			},
		},
		{
			name: "When GPUs capability is absent it should default to 0",
			input: makeSKU("Standard_B2s", "virtualMachines", map[string]string{
				"vCPUs":               "2",
				"MemoryGB":            "4",
				"CpuArchitectureType": "x64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_B2s",
				VCPU:            2,
				MemoryMb:        4096,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When MemoryGB is fractional it should convert correctly",
			input: makeSKU("Standard_B1ls", "virtualMachines", map[string]string{
				"vCPUs":               "1",
				"MemoryGB":            "0.5",
				"CpuArchitectureType": "x64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_B1ls",
				VCPU:            1,
				MemoryMb:        512,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When MemoryGB is large it should convert correctly",
			input: makeSKU("Standard_M416ms_v2", "virtualMachines", map[string]string{
				"vCPUs":               "416",
				"MemoryGB":            "11400",
				"CpuArchitectureType": "x64",
			}),
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_M416ms_v2",
				VCPU:            416,
				MemoryMb:        11673600,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result, err := transformSKU(tt.input)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestTransformSKU_WhenMissingRequiredFields_ItShouldReturnError(t *testing.T) {
	tests := []struct {
		name          string
		input         *armcompute.ResourceSKU
		expectedError string
	}{
		{
			name: "When SKU name is nil it should return error",
			input: &armcompute.ResourceSKU{
				Name:         nil,
				ResourceType: to.Ptr("virtualMachines"),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: to.Ptr("vCPUs"), Value: to.Ptr("4")},
				},
			},
			expectedError: "SKU name is missing",
		},
		{
			name: "When vCPUs capability is missing it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"MemoryGB":            "16",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "missing vCPUs capability",
		},
		{
			name: "When MemoryGB capability is missing it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "missing MemoryGB capability",
		},
		{
			name: "When CpuArchitectureType capability is missing it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":    "4",
				"MemoryGB": "16",
			}),
			expectedError: "missing CpuArchitectureType capability",
		},
		{
			name: "When vCPUs value is not a valid integer it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "abc",
				"MemoryGB":            "16",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "invalid vCPUs value",
		},
		{
			name: "When MemoryGB value is not a valid float it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"MemoryGB":            "xyz",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "invalid MemoryGB value",
		},
		{
			name: "When vCPUs value is zero it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "0",
				"MemoryGB":            "16",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "invalid vCPUs count",
		},
		{
			name: "When MemoryGB value is zero it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"MemoryGB":            "0",
				"CpuArchitectureType": "x64",
			}),
			expectedError: "invalid MemoryGB value",
		},
		{
			name: "When CpuArchitectureType is unsupported it should return error",
			input: makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
				"vCPUs":               "4",
				"MemoryGB":            "16",
				"CpuArchitectureType": "i386",
			}),
			expectedError: "unsupported CPU architecture",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			_, err := transformSKU(tt.input)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tt.expectedError))
		})
	}
}

func TestGetInstanceTypeInfo(t *testing.T) {
	tests := []struct {
		name          string
		skus          []*armcompute.ResourceSKU
		apiErr        error
		instanceType  string
		expected      *instancetype.InstanceTypeInfo
		expectedError string
	}{
		{
			name: "When VM size exists it should return info",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
					"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "x64",
				}),
			},
			instanceType: "Standard_D4s_v3",
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_D4s_v3",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When VM size not found it should return error",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
					"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "x64",
				}),
			},
			instanceType:  "Standard_Nonexistent",
			expectedError: "not found",
		},
		{
			name:          "When API returns error it should propagate error",
			apiErr:        fmt.Errorf("API error: throttling"),
			instanceType:  "Standard_D4s_v3",
			expectedError: "failed to load Azure Resource SKUs",
		},
		{
			name: "When SKU has matching name but wrong ResourceType it should return not found",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v3", "disks", map[string]string{
					"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "x64",
				}),
			},
			instanceType:  "Standard_D4s_v3",
			expectedError: "not found",
		},
		{
			name: "When multiple SKUs returned it should match only virtualMachines type",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v3", "disks", map[string]string{
					"vCPUs": "99", "MemoryGB": "99", "CpuArchitectureType": "x64",
				}),
				makeSKU("Standard_D4s_v3", "virtualMachines", map[string]string{
					"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "x64",
				}),
			},
			instanceType: "Standard_D4s_v3",
			expected: &instancetype.InstanceTypeInfo{
				InstanceType:    "Standard_D4s_v3",
				VCPU:            4,
				MemoryMb:        16384,
				GPU:             0,
				CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mock := &mockResourceSKUsAPI{skus: tt.skus, err: tt.apiErr}
			provider := NewProvider(mock, "eastus")
			result, err := provider.GetInstanceTypeInfo(context.Background(), tt.instanceType)

			if tt.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectedError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(Equal(tt.expected))
			}
		})
	}
}

func TestGetCapabilityValue(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []*armcompute.ResourceSKUCapabilities
		capName      string
		expectedVal  string
		expectedOK   bool
	}{
		{
			name: "When capability exists it should return the value",
			capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: to.Ptr("vCPUs"), Value: to.Ptr("4")},
			},
			capName:     "vCPUs",
			expectedVal: "4",
			expectedOK:  true,
		},
		{
			name: "When capability does not exist it should return empty and false",
			capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: to.Ptr("vCPUs"), Value: to.Ptr("4")},
			},
			capName:     "GPUs",
			expectedVal: "",
			expectedOK:  false,
		},
		{
			name:         "When capabilities slice is nil it should return empty and false",
			capabilities: nil,
			capName:      "vCPUs",
			expectedVal:  "",
			expectedOK:   false,
		},
		{
			name: "When capability name has different case it should not match",
			capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: to.Ptr("vcpus"), Value: to.Ptr("4")},
			},
			capName:     "vCPUs",
			expectedVal: "",
			expectedOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			val, ok := getCapabilityValue(tt.capabilities, tt.capName)
			g.Expect(ok).To(Equal(tt.expectedOK))
			g.Expect(val).To(Equal(tt.expectedVal))
		})
	}
}
