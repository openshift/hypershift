package azure

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// ResourceSKUsAPI defines the operations used from armcompute.ResourceSKUsClient.
type ResourceSKUsAPI interface {
	NewListPager(options *armcompute.ResourceSKUsClientListOptions) *azruntime.Pager[armcompute.ResourceSKUsClientListResponse]
}

// Compile-time check that the real client satisfies our interface.
var _ ResourceSKUsAPI = (*armcompute.ResourceSKUsClient)(nil)

// Provider implements the instancetype.Provider interface for Azure.
// It queries the Azure Resource SKUs API to get VM size specifications.
type Provider struct {
	skuClient ResourceSKUsAPI
	location  string
	cache     map[string]*instancetype.InstanceTypeInfo
	mu        sync.Mutex
}

// NewProvider creates a new Azure instance type provider.
func NewProvider(skuClient ResourceSKUsAPI, location string) *Provider {
	return &Provider{
		skuClient: skuClient,
		location:  location,
	}
}

// GetInstanceTypeInfo queries Azure Resource SKUs API for VM size specifications.
func (p *Provider) GetInstanceTypeInfo(ctx context.Context, instanceType string) (*instancetype.InstanceTypeInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cache == nil {
		if err := p.loadSKUs(ctx); err != nil {
			return nil, fmt.Errorf("failed to load Azure Resource SKUs: %w", err)
		}
	}

	info, ok := p.cache[instanceType]
	if !ok {
		return nil, fmt.Errorf("VM size %q not found in Azure Resource SKUs for location %q", instanceType, p.location)
	}

	copied := *info
	return &copied, nil
}

func (p *Provider) loadSKUs(ctx context.Context) error {
	nextCache := make(map[string]*instancetype.InstanceTypeInfo)

	filter := fmt.Sprintf("location eq '%s'", p.location)
	pager := p.skuClient.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list Azure Resource SKUs: %w", err)
		}

		for _, sku := range page.Value {
			if sku.ResourceType == nil || !strings.EqualFold(*sku.ResourceType, "virtualMachines") {
				continue
			}

			info, err := transformSKU(sku)
			if err != nil {
				continue
			}
			nextCache[info.InstanceType] = info
		}
	}

	p.cache = nextCache
	return nil
}

func transformSKU(sku *armcompute.ResourceSKU) (*instancetype.InstanceTypeInfo, error) {
	if sku.Name == nil || *sku.Name == "" {
		return nil, fmt.Errorf("SKU name is missing")
	}

	name := *sku.Name
	info := &instancetype.InstanceTypeInfo{
		InstanceType: name,
	}

	vcpuStr, ok := getCapabilityValue(sku.Capabilities, "vCPUs")
	if !ok {
		return nil, fmt.Errorf("missing vCPUs capability for VM size %q", name)
	}
	vcpu, err := strconv.ParseInt(vcpuStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid vCPUs value %q for VM size %q: %w", vcpuStr, name, err)
	}
	if vcpu <= 0 {
		return nil, fmt.Errorf("invalid vCPUs count %d for VM size %q", vcpu, name)
	}
	info.VCPU = int32(vcpu)

	memStr, ok := getCapabilityValue(sku.Capabilities, "MemoryGB")
	if !ok {
		return nil, fmt.Errorf("missing MemoryGB capability for VM size %q", name)
	}
	memGB, err := strconv.ParseFloat(memStr, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid MemoryGB value %q for VM size %q: %w", memStr, name, err)
	}
	if memGB <= 0 {
		return nil, fmt.Errorf("invalid MemoryGB value %v for VM size %q", memGB, name)
	}
	info.MemoryMb = int64(math.Round(memGB * 1024))

	gpuStr, ok := getCapabilityValue(sku.Capabilities, "GPUs")
	if ok {
		gpu, err := strconv.ParseInt(gpuStr, 10, 32)
		if err == nil {
			info.GPU = int32(gpu)
		}
	}

	archStr, ok := getCapabilityValue(sku.Capabilities, "CpuArchitectureType")
	if !ok {
		return nil, fmt.Errorf("missing CpuArchitectureType capability for VM size %q", name)
	}
	switch strings.ToLower(archStr) {
	case "x64":
		info.CPUArchitecture = hyperv1.ArchitectureAMD64
	case "arm64":
		info.CPUArchitecture = hyperv1.ArchitectureARM64
	default:
		return nil, fmt.Errorf("unsupported CPU architecture %q for VM size %q", archStr, name)
	}

	return info, nil
}

func getCapabilityValue(capabilities []*armcompute.ResourceSKUCapabilities, name string) (string, bool) {
	for _, cap := range capabilities {
		if cap.Name != nil && *cap.Name == name {
			if cap.Value != nil {
				return *cap.Value, true
			}
			return "", false
		}
	}
	return "", false
}
