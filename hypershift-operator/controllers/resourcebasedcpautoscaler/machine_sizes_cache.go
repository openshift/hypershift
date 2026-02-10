package resourcebasedcpautoscaler

import (
	"fmt"
	"slices"
	"sync"

	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
)

// machineSizesCache provides a thread-safe cache of machine sizes for cluster sizing decisions.
// It can populate size information either from ClusterSizingConfiguration capacity specs or by
// introspecting existing MachineSets in the management cluster.
//
// The cache tracks:
// - Machine memory and CPU resources for each cluster size label
// - The KubeAPIServer memory fraction used for sizing calculations
// - The ClusterSizingConfiguration generation to detect updates
//
// Usage:
//
//	cache := machineSizesCache{}
//	err := cache.update(config, listMachineSetsFn, logger)
//	recommendedSize := cache.recommendedSize(memoryBytes)
type machineSizesCache struct {
	// cscGeneration tracks the ClusterSizingConfiguration generation to detect updates
	cscGeneration int64

	// kasMemorySizeFraction specifies what fraction of machine memory should be
	// considered available for the KubeAPIServer container. If nil, uses the default.
	kasMemorySizeFraction *resource.Quantity

	// sizes maps cluster size labels to their machine resource specifications
	sizes map[string]machineResources

	// m protects concurrent access to the cache fields
	m sync.Mutex
}

// machineResources represents the CPU and memory resources available on a machine
// of a particular cluster size.
type machineResources struct {
	Memory resource.Quantity
	CPU    resource.Quantity
}

// update refreshes the cache with the latest ClusterSizingConfiguration and machine information.
// It will skip the update if the configuration generation hasn't changed and capacity data is
// available from the config itself.
//
// The update process:
// 1. Validates any provided KubeAPIServerMemoryFraction
// 2. If capacity data is available in config, uses that directly
// 3. Otherwise, introspects MachineSets to determine machine sizes
func (s *machineSizesCache) update(csc *schedulingv1alpha1.ClusterSizingConfiguration, listMachineSets func() (*machinev1beta1.MachineSetList, error), log logr.Logger) error {
	s.m.Lock()
	defer s.m.Unlock()

	if csc.Generation == s.cscGeneration && sizeMemoryCapacityAvailable(csc) {
		// machine sizes are up to date - cache already populated for this generation
		return nil
	}

	log.Info("Updating machine size cache")

	// Validate KubeAPIServerMemoryFraction if provided
	kasMemorySizeFraction := csc.Spec.ResourceBasedAutoscaling.KubeAPIServerMemoryFraction
	if kasMemorySizeFraction != nil {
		fraction := kasMemorySizeFraction.AsApproximateFloat64()
		if fraction <= 0 || fraction > 1 {
			return fmt.Errorf("KubeAPIServerMemoryFraction must be between 0 and 1, got %f", fraction)
		}
	}
	s.kasMemorySizeFraction = kasMemorySizeFraction

	if sizeMemoryCapacityAvailable(csc) {
		s.updateSizesFromConfig(csc)
		return nil
	}

	return s.updateSizesFromMachineSets(csc, listMachineSets)
}

// sizeMemoryCapacityAvailable checks if the ClusterSizingConfiguration contains
// memory capacity information for all configured sizes.
func sizeMemoryCapacityAvailable(csc *schedulingv1alpha1.ClusterSizingConfiguration) bool {
	for _, size := range csc.Spec.Sizes {
		if size.Capacity == nil {
			return false
		}
		if size.Capacity.Memory == nil {
			return false
		}
	}
	return true
}

// updateSizesFromConfig populates the cache using capacity information directly
// from the ClusterSizingConfiguration.
func (s *machineSizesCache) updateSizesFromConfig(csc *schedulingv1alpha1.ClusterSizingConfiguration) {
	sizes := map[string]machineResources{}
	for _, sizeConfig := range csc.Spec.Sizes {
		resources := machineResources{
			Memory: ptr.Deref(sizeConfig.Capacity.Memory, *resource.NewQuantity(0, resource.DecimalSI)),
			CPU:    ptr.Deref(sizeConfig.Capacity.CPU, *resource.NewQuantity(0, resource.DecimalSI)),
		}
		sizes[sizeConfig.Name] = resources
	}
	s.sizes = sizes
	s.cscGeneration = csc.Generation
}

// updateSizesFromMachineSets introspects existing MachineSets to determine machine
// sizes for each cluster size label. It reads machine specifications from MachineSet
// annotations (machine.openshift.io/memoryMb and machine.openshift.io/vCPU).
//
// Note: The first MachineSet found for each size label becomes the authoritative
// source. If instance types and size labels are inconsistent, results may be unpredictable.
func (s *machineSizesCache) updateSizesFromMachineSets(csc *schedulingv1alpha1.ClusterSizingConfiguration, listMachineSets func() (*machinev1beta1.MachineSetList, error)) error {
	machineSetList, err := listMachineSets()
	if err != nil {
		return err
	}
	sizes := map[string]machineResources{}
	hasAllSizes := func() bool {
		for _, size := range csc.Spec.Sizes {
			if _, hasSize := sizes[size.Name]; !hasSize {
				return false
			}
		}
		return true
	}

	for _, ms := range machineSetList.Items {
		clusterSize := ms.Spec.Template.Spec.ObjectMeta.Labels["hypershift.openshift.io/cluster-size"]
		if clusterSize == "" {
			continue
		}
		// The first machineset that matches a given size label is used as the authoritative source for
		// machine sizes of that label. If instance types and cluster size labels are not consistent, the
		// size stored in this cache will be unpredictable.
		if _, exists := sizes[clusterSize]; exists {
			continue
		}
		memoryInMB, hasMemory := ms.Annotations["machine.openshift.io/memoryMb"]
		if !hasMemory {
			continue
		}
		vCPU, hasCPU := ms.Annotations["machine.openshift.io/vCPU"]
		if !hasCPU {
			continue
		}

		memory, err := resource.ParseQuantity(memoryInMB + "Mi")
		if err != nil {
			continue
		}
		cpu, err := resource.ParseQuantity(vCPU)
		if err != nil {
			continue
		}
		resources := machineResources{
			Memory: memory,
			CPU:    cpu,
		}
		sizes[clusterSize] = resources
		if hasAllSizes() {
			break
		}
	}
	if !hasAllSizes() {
		return fmt.Errorf("failed to introspect all machine sizes with existing machinesets")
	}
	s.sizes = sizes
	s.cscGeneration = csc.Generation
	return nil
}

// kasMemoryFraction returns the fraction of machine memory that should be considered
// available for the KubeAPIServer container. Uses the configured value if set,
// otherwise returns the default.
func (s *machineSizesCache) kasMemoryFraction() float64 {
	if s.kasMemorySizeFraction != nil {
		return s.kasMemorySizeFraction.AsApproximateFloat64()
	}
	return kubeAPIServerMemorySizeFractionDefault
}

// sizesInOrderByMemory returns all cached cluster size labels sorted by
// memory capacity in ascending order.
func (s *machineSizesCache) sizesInOrderByMemory() []string {
	type sizeWithMemory struct {
		size   string
		memory resource.Quantity
	}
	sizesToOrder := make([]sizeWithMemory, 0, len(s.sizes))
	for size, resources := range s.sizes {
		sizesToOrder = append(sizesToOrder, sizeWithMemory{
			size:   size,
			memory: resources.Memory,
		})
	}
	slices.SortFunc(sizesToOrder, func(a, b sizeWithMemory) int {
		return a.memory.Cmp(b.memory)
	})
	sortedSizes := make([]string, len(sizesToOrder))
	for i := range sizesToOrder {
		sortedSizes[i] = sizesToOrder[i].size
	}
	return sortedSizes
}

// recommendedSize returns the smallest cluster size that can accommodate the given
// memory requirement for the KubeAPIServer container.
//
// The calculation considers the KubeAPIServer memory fraction to determine the
// effective memory available on each machine size. If no size is large enough,
// returns the largest available size as a best effort.
//
// Returns an empty string if no sizes are cached.
func (s *machineSizesCache) recommendedSize(memory float64) string {
	s.m.Lock()
	defer s.m.Unlock()

	sizesInOrder := s.sizesInOrderByMemory()
	if len(sizesInOrder) == 0 {
		return ""
	}
	for _, size := range sizesInOrder {
		resources, hasSize := s.sizes[size]
		if !hasSize {
			continue
		}
		containerMemoryCapacity := resources.Memory.AsApproximateFloat64() * s.kasMemoryFraction()
		if containerMemoryCapacity >= memory {
			return size
		}
	}
	// Best effort: return the largest cluster size
	return sizesInOrder[len(sizesInOrder)-1]
}
