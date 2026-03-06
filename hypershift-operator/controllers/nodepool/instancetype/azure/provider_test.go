package azure

import (
	"context"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"k8s.io/utils/ptr"
)

// mockSKUClient implements SKUClient interface for testing
type mockSKUClient struct {
	newListPagerFunc func(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse]
}

func (m *mockSKUClient) NewListPager(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
	if m.newListPagerFunc != nil {
		return m.newListPagerFunc(options)
	}
	return nil
}

func makeSKU(name, resourceType string, capabilities map[string]string) *armcompute.ResourceSKU {
	sku := &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		ResourceType: ptr.To(resourceType),
	}
	if len(capabilities) > 0 {
		caps := make([]*armcompute.ResourceSKUCapabilities, 0, len(capabilities))
		for k, v := range capabilities {
			caps = append(caps, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(k),
				Value: ptr.To(v),
			})
		}
		sku.Capabilities = caps
	}
	return sku
}

func makePager(skus ...*armcompute.ResourceSKU) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
	page := armcompute.ResourceSKUsClientListResponse{
		ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
	}
	called := false
	return runtime.NewPager(runtime.PagingHandler[armcompute.ResourceSKUsClientListResponse]{
		More: func(p armcompute.ResourceSKUsClientListResponse) bool {
			return !called
		},
		Fetcher: func(ctx context.Context, p *armcompute.ResourceSKUsClientListResponse) (armcompute.ResourceSKUsClientListResponse, error) {
			called = true
			return page, nil
		},
	})
}

func TestTransformSKU(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sku     *armcompute.ResourceSKU
		want    *instancetype.InstanceTypeInfo
		wantErr string
	}{
		{
			name: "When transforming a valid AMD64 SKU, it should return correct instance type info",
			sku: makeSKU("Standard_D4s_v5", "virtualMachines", map[string]string{
				"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "x64",
			}),
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_D4s_v5", VCPU: 4, MemoryMb: 16384, CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name: "When transforming a valid ARM64 SKU with GPU, it should return correct instance type info",
			sku: makeSKU("Standard_NC6s_v3", "virtualMachines", map[string]string{
				"vCPUs": "6", "MemoryGB": "112", "GPUs": "1", "CpuArchitectureType": "Arm64",
			}),
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_NC6s_v3", VCPU: 6, MemoryMb: 114688, GPU: 1, CPUArchitecture: hyperv1.ArchitectureARM64,
			},
		},
		{
			name: "When architecture is missing, it should default to AMD64",
			sku:  makeSKU("Standard_D2s_v3", "virtualMachines", map[string]string{"vCPUs": "2", "MemoryGB": "8"}),
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_D2s_v3", VCPU: 2, MemoryMb: 8192, CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:    "When SKU is nil, it should return an error",
			sku:     nil,
			wantErr: "SKU or SKU name is nil",
		},
		{
			name:    "When capabilities are nil, it should return an error",
			sku:     &armcompute.ResourceSKU{Name: ptr.To("test")},
			wantErr: "has no capabilities",
		},
		{
			name:    "When a required capability is missing, it should return an error",
			sku:     makeSKU("test", "virtualMachines", map[string]string{"MemoryGB": "16"}),
			wantErr: "vCPUs capability not found",
		},
		{
			name:    "When a capability value is invalid, it should return an error",
			sku:     makeSKU("test", "virtualMachines", map[string]string{"vCPUs": "x", "MemoryGB": "16"}),
			wantErr: "failed to parse vCPUs",
		},
		{
			name:    "When architecture is unsupported, it should return an error",
			sku:     makeSKU("test", "virtualMachines", map[string]string{"vCPUs": "4", "MemoryGB": "16", "CpuArchitectureType": "riscv"}),
			wantErr: "unsupported CPU architecture",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := transformSKU(tt.sku)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("transformSKU() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("transformSKU() unexpected error = %v", err)
				return
			}
			if got.InstanceType != tt.want.InstanceType || got.VCPU != tt.want.VCPU ||
				got.MemoryMb != tt.want.MemoryMb || got.GPU != tt.want.GPU || got.CPUArchitecture != tt.want.CPUArchitecture {
				t.Errorf("transformSKU() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetInstanceTypeInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		vmSize  string
		skus    []*armcompute.ResourceSKU
		want    *instancetype.InstanceTypeInfo
		wantErr string
	}{
		{
			name:   "When a matching VM exists, it should return the correct instance type info",
			vmSize: "Standard_D4s_v5",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D2s_v5", "virtualMachines", map[string]string{"vCPUs": "2", "MemoryGB": "8"}),
				makeSKU("Standard_D4s_v5", "virtualMachines", map[string]string{"vCPUs": "4", "MemoryGB": "16"}),
			},
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_D4s_v5", VCPU: 4, MemoryMb: 16384, CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:   "When VM size has different casing, it should match case-insensitively",
			vmSize: "standard_d4s_v5",
			skus:   []*armcompute.ResourceSKU{makeSKU("Standard_D4s_v5", "virtualMachines", map[string]string{"vCPUs": "4", "MemoryGB": "16"})},
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_D4s_v5", VCPU: 4, MemoryMb: 16384, CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:   "When non-virtualMachines SKUs exist, it should skip them and find the correct VM",
			vmSize: "Standard_D4s_v5",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v5", "disks", map[string]string{"vCPUs": "4", "MemoryGB": "16"}),
				makeSKU("Standard_D4s_v5", "virtualMachines", map[string]string{"vCPUs": "4", "MemoryGB": "16"}),
			},
			want: &instancetype.InstanceTypeInfo{
				InstanceType: "Standard_D4s_v5", VCPU: 4, MemoryMb: 16384, CPUArchitecture: hyperv1.ArchitectureAMD64,
			},
		},
		{
			name:    "When the VM size does not exist, it should return an error",
			vmSize:  "Standard_NonExistent",
			skus:    []*armcompute.ResourceSKU{makeSKU("Standard_D2s_v5", "virtualMachines", map[string]string{"vCPUs": "2", "MemoryGB": "8"})},
			wantErr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := NewProvider(&mockSKUClient{
				newListPagerFunc: func(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
					return makePager(tt.skus...)
				},
			})

			got, err := provider.GetInstanceTypeInfo(context.Background(), tt.vmSize)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("GetInstanceTypeInfo() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("GetInstanceTypeInfo() unexpected error = %v", err)
				return
			}
			if got.InstanceType != tt.want.InstanceType || got.VCPU != tt.want.VCPU ||
				got.MemoryMb != tt.want.MemoryMb || got.CPUArchitecture != tt.want.CPUArchitecture {
				t.Errorf("GetInstanceTypeInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
