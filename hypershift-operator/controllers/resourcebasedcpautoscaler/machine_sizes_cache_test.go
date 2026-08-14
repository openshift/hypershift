package resourcebasedcpautoscaler

import (
	"testing"

	. "github.com/onsi/gomega"

	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/go-logr/stdr"
)

func TestMachineSizesCache(t *testing.T) {
	g := NewGomegaWithT(t)
	cache := machineSizesCache{}
	log := stdr.New(nil)

	ms := func(sizeLabel, memorySize string) machinev1beta1.MachineSet {
		result := machinev1beta1.MachineSet{}
		result.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{"hypershift.openshift.io/cluster-size": sizeLabel}
		result.Annotations = map[string]string{
			"machine.openshift.io/memoryMb": memorySize,
			"machine.openshift.io/vCPU":     "2", // not relevant for now
		}
		return result
	}

	listMachineSets := func() (*machinev1beta1.MachineSetList, error) {
		return &machinev1beta1.MachineSetList{
			Items: []machinev1beta1.MachineSet{ms("small", "4096"), ms("medium", "8192"), ms("large", "16384")},
		}, nil
	}
	csc := defaultSizingConfigWithCapacity()
	err := cache.update(csc, listMachineSets, log)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cache.sizesInOrderByMemory()).To(Equal([]string{"small", "medium", "large"}))
	g.Expect(cache.sizes["medium"].Memory).To(Equal(resource.MustParse("8192Mi")))

	failOnCall := func() (*machinev1beta1.MachineSetList, error) {
		g.Fail("this function should not be called")
		return nil, nil
	}
	// ensure if update is called again with the same csc generation, machinesets are not listed again
	err = cache.update(csc, failOnCall, log)
	g.Expect(err).ToNot(HaveOccurred())
}

func TestMachineSizesCacheWithSizingConfig(t *testing.T) {
	g := NewGomegaWithT(t)
	log := stdr.New(nil)
	csc := hostedclustersizing.DefaultSizingConfig()
	csc.Generation = 1
	size := resource.MustParse("8Gi")
	for i := range csc.Spec.Sizes {
		csc.Spec.Sizes[i].Capacity = &schedulingv1alpha1.SizeCapacity{
			Memory: ptr.To(size),
		}
		size.Mul(2)
	}
	failOnCall := func() (*machinev1beta1.MachineSetList, error) {
		g.Fail("this function should not be called")
		return nil, nil
	}
	cache := machineSizesCache{}
	err := cache.update(csc, failOnCall, log)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cache.sizesInOrderByMemory()).To(Equal([]string{"small", "medium", "large"}))
	g.Expect(cache.sizes["small"].Memory).To(Equal(resource.MustParse("8Gi")))
}

func TestMachineSizesCacheRecommendedSize(t *testing.T) {
	g := NewGomegaWithT(t)

	cache := machineSizesCache{
		sizes: map[string]machineResources{
			"small": {
				Memory: resource.MustParse("4Gi"),
			},
			"medium": {
				Memory: resource.MustParse("8Gi"),
			},
			"large": {
				Memory: resource.MustParse("16Gi"),
			},
		},
	}

	// Test that it returns the smallest size that can accommodate the memory requirement
	g.Expect(cache.recommendedSize(2.0 * 1024 * 1024 * 1024)).To(Equal("small"))  // 2Gi should fit in small (4Gi * 0.65)
	g.Expect(cache.recommendedSize(5.0 * 1024 * 1024 * 1024)).To(Equal("medium")) // 5Gi should need medium
	g.Expect(cache.recommendedSize(12.0 * 1024 * 1024 * 1024)).To(Equal("large")) // 12Gi should need large
	g.Expect(cache.recommendedSize(20.0 * 1024 * 1024 * 1024)).To(Equal("large")) // Over capacity should return largest

	// Test empty cache
	emptyCache := machineSizesCache{}
	g.Expect(emptyCache.recommendedSize(1.0)).To(Equal(""))
}

func TestMachineSizesCacheMemoryFraction(t *testing.T) {
	g := NewGomegaWithT(t)

	// Test default fraction
	cache := machineSizesCache{}
	g.Expect(cache.kasMemoryFraction()).To(Equal(kubeAPIServerMemorySizeFractionDefault))

	// Test custom fraction
	customFraction := resource.MustParse("0.8")
	cache.kasMemorySizeFraction = &customFraction
	g.Expect(cache.kasMemoryFraction()).To(BeNumerically("~", 0.8, 0.0001))
}

func TestMachineSizesCacheValidation(t *testing.T) {
	g := NewGomegaWithT(t)
	log := stdr.New(nil)
	cache := machineSizesCache{}

	// Test invalid memory fraction (too high)
	csc := defaultSizingConfigWithCapacity()
	invalidFraction := resource.MustParse("1.5")
	csc.Spec.ResourceBasedAutoscaling.KubeAPIServerMemoryFraction = &invalidFraction

	err := cache.update(csc, nil, log)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerMemoryFraction must be between 0 and 1"))

	// Test invalid memory fraction (too low)
	negativeiFraction := resource.MustParse("-0.1")
	csc.Spec.ResourceBasedAutoscaling.KubeAPIServerMemoryFraction = &negativeiFraction

	err = cache.update(csc, nil, log)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerMemoryFraction must be between 0 and 1"))

	// Test valid memory fraction
	validFraction := resource.MustParse("0.7")
	csc.Spec.ResourceBasedAutoscaling.KubeAPIServerMemoryFraction = &validFraction

	err = cache.update(csc, func() (*machinev1beta1.MachineSetList, error) { return &machinev1beta1.MachineSetList{}, nil }, log)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cache.kasMemoryFraction()).To(BeNumerically("~", 0.7, 0.0001))
}
