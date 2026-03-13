package azure

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

// SKUClient interface for Azure SKU operations.
// Azure SDK v5 doesn't provide interface types (unlike AWS SDK's ec2iface package),
// so we define this minimal interface to enable dependency injection and testing.
type SKUClient interface {
	NewListPager(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse]
}

// Provider implements the instancetype.Provider interface for Azure.
// It queries Azure Resource SKUs API to get VM size specifications
// and caches results to avoid repeated API calls during reconciliation.
type Provider struct {
	skuClient SKUClient
	cache     sync.Map
}

// NewProvider creates a new Azure instance type provider with the given SKU client.
// The caller is responsible for creating the SKU client with the correct credentials and subscription.
func NewProvider(skuClient SKUClient) *Provider {
	return &Provider{
		skuClient: skuClient,
	}
}

// GetInstanceTypeInfo queries Azure Resource SKUs API for VM size specifications.
// This information is used to populate cluster autoscaler capacity annotations
// for scaling from zero replicas.
// Note: VM size specifications (CPU, memory, GPU) are consistent across all Azure locations,
// so we don't need to filter by location.
func (p *Provider) GetInstanceTypeInfo(ctx context.Context, vmSize string) (*instancetype.InstanceTypeInfo, error) {
	cacheKey := strings.ToLower(vmSize)

	// Return cached result if available
	if cached, ok := p.cache.Load(cacheKey); ok {
		return cached.(*instancetype.InstanceTypeInfo), nil
	}

	// Cache miss — query Azure Resource SKUs API.
	// Filter by resourceType to reduce response size; name matching is done in code since the API doesn't support it.
	filter := "resourceType eq 'virtualMachines'"
	pager := p.skuClient.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list Azure SKUs: %w", err)
		}

		for _, sku := range page.Value {
			if sku == nil || sku.Name == nil {
				continue
			}

			// Match VM size (case-insensitive comparison)
			if !strings.EqualFold(*sku.Name, vmSize) {
				continue
			}

			// Verify this is a VM SKU
			if sku.ResourceType == nil || *sku.ResourceType != "virtualMachines" {
				continue
			}

			// Transform SKU to InstanceTypeInfo
			// Note: We take the first matching SKU since VM specs are consistent across locations
			result, err := transformSKU(sku)
			if err != nil {
				return nil, fmt.Errorf("failed to transform SKU %q: %w", vmSize, err)
			}

			p.cache.Store(cacheKey, result)
			return result, nil
		}
	}

	return nil, fmt.Errorf("VM size %q not found", vmSize)
}

// transformSKU converts Azure ResourceSKU to our common InstanceTypeInfo structure
func transformSKU(sku *armcompute.ResourceSKU) (*instancetype.InstanceTypeInfo, error) {
	if sku == nil || sku.Name == nil {
		return nil, fmt.Errorf("SKU or SKU name is nil")
	}

	info := &instancetype.InstanceTypeInfo{
		InstanceType: *sku.Name,
	}

	// Parse capabilities to extract vCPUs, memory, and GPUs
	if sku.Capabilities == nil {
		return nil, fmt.Errorf("SKU %q has no capabilities", *sku.Name)
	}

	var vcpuFound, memoryFound, archFound bool

	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}

		switch *capability.Name {
		case "vCPUs":
			vcpu, err := parseIntCapability(*capability.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse vCPUs for SKU %q: %w", *sku.Name, err)
			}
			if vcpu <= 0 {
				return nil, fmt.Errorf("invalid vCPU count %d for SKU %q", vcpu, *sku.Name)
			}
			info.VCPU = int32(vcpu)
			vcpuFound = true

		case "MemoryGB":
			memoryGB, err := parseFloatCapability(*capability.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse memory for SKU %q: %w", *sku.Name, err)
			}
			if memoryGB <= 0 {
				return nil, fmt.Errorf("invalid memory size %f for SKU %q", memoryGB, *sku.Name)
			}
			// Convert GiB to MiB (Azure's MemoryGB capability actually returns GiB values)
			info.MemoryMb = int64(memoryGB * 1024)
			memoryFound = true

		case "GPUs":
			gpu, err := parseIntCapability(*capability.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse GPUs for SKU %q: %w", *sku.Name, err)
			}
			info.GPU = int32(gpu)

		case "CpuArchitectureType":
			archFound = true
			switch *capability.Value {
			case string(armcompute.ArchitectureX64):
				info.CPUArchitecture = hyperv1.ArchitectureAMD64
			case string(armcompute.ArchitectureArm64):
				info.CPUArchitecture = hyperv1.ArchitectureARM64
			default:
				return nil, fmt.Errorf("unsupported CPU architecture %q for SKU %q", *capability.Value, *sku.Name)
			}
		}
	}

	// Validate required fields
	if !vcpuFound {
		return nil, fmt.Errorf("vCPUs capability not found for SKU %q", *sku.Name)
	}
	if !memoryFound {
		return nil, fmt.Errorf("memory capability not found for SKU %q", *sku.Name)
	}
	if !archFound {
		// Default to amd64 if architecture is not specified (most Azure VMs are x64)
		info.CPUArchitecture = hyperv1.ArchitectureAMD64
	}

	return info, nil
}

// parseIntCapability parses an integer capability value
func parseIntCapability(value string) (int, error) {
	result, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid integer value %q: %w", value, err)
	}
	return result, nil
}

// parseFloatCapability parses a float capability value
func parseFloatCapability(value string) (float64, error) {
	result, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float value %q: %w", value, err)
	}
	return result, nil
}
