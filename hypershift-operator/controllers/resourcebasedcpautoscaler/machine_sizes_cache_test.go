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

func TestMachineSizesCacheCPUFraction(t *testing.T) {
	t.Run("When no CPU fraction is configured it should return the default", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		g.Expect(cache.kasCPUFraction()).To(Equal(kubeAPIServerCPUSizeFractionDefault))
	})

	t.Run("When a custom CPU fraction is configured it should return the custom value", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		customFraction := resource.MustParse("0.5")
		cache.kasCPUSizeFraction = &customFraction
		g.Expect(cache.kasCPUFraction()).To(BeNumerically("~", 0.5, 0.0001))
	})
}

func TestMachineSizesCacheCPUFractionValidation(t *testing.T) {
	log := stdr.New(nil)

	t.Run("When CPU fraction is too high it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		invalidFraction := resource.MustParse("1.5")
		csc.Spec.ResourceBasedAutoscaling.KubeAPIServerCPUFraction = &invalidFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerCPUFraction must be between 0 and 1"))
	})

	t.Run("When CPU fraction is negative it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		negativeFraction := resource.MustParse("-0.1")
		csc.Spec.ResourceBasedAutoscaling.KubeAPIServerCPUFraction = &negativeFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerCPUFraction must be between 0 and 1"))
	})

	t.Run("When CPU fraction is valid it should store the value", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		validFraction := resource.MustParse("0.5")
		csc.Spec.ResourceBasedAutoscaling.KubeAPIServerCPUFraction = &validFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cache.kasCPUFraction()).To(BeNumerically("~", 0.5, 0.0001))
	})
}

func TestRecommendedSizeByCPU(t *testing.T) {
	t.Run("When CPU fits in small it should return small", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
		}
		// 2 CPU should fit in small (4 * 0.65 = 2.6)
		g.Expect(cache.recommendedSizeByCPU(2.0)).To(Equal("small"))
	})

	t.Run("When CPU needs medium it should return medium", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
		}
		// 4 CPU should need medium (4 * 0.65 = 2.6 < 4, but 8 * 0.65 = 5.2 >= 4)
		g.Expect(cache.recommendedSizeByCPU(4.0)).To(Equal("medium"))
	})

	t.Run("When CPU exceeds all sizes it should return the largest size", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
		}
		// 20 CPU exceeds all sizes
		g.Expect(cache.recommendedSizeByCPU(20.0)).To(Equal("large"))
	})

	t.Run("When cache is empty it should return empty string", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		g.Expect(cache.recommendedSizeByCPU(1.0)).To(Equal(""))
	})
}

func TestRecommendedSizeByBoth(t *testing.T) {
	newCacheWithCPUAndMemory := func() machineSizesCache {
		return machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
		}
	}

	t.Run("When memory needs small and CPU needs small it should return small", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := newCacheWithCPUAndMemory()
		// memory: 2Gi fits in small (4Gi * 0.65 = 2.6Gi), CPU: 2 fits in small (4 * 0.65 = 2.6)
		g.Expect(cache.recommendedSizeByBoth(2.0*1024*1024*1024, 2.0)).To(Equal("small"))
	})

	t.Run("When CPU drives a larger size than memory it should return the CPU-driven size", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := newCacheWithCPUAndMemory()
		// memory: 2Gi fits in small, CPU: 4 needs medium (4 * 0.65 = 2.6 < 4)
		g.Expect(cache.recommendedSizeByBoth(2.0*1024*1024*1024, 4.0)).To(Equal("medium"))
	})

	t.Run("When memory drives a larger size than CPU it should return the memory-driven size", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := newCacheWithCPUAndMemory()
		// memory: 5Gi needs medium (4Gi * 0.65 = 2.6 < 5), CPU: 2 fits in small
		g.Expect(cache.recommendedSizeByBoth(5.0*1024*1024*1024, 2.0)).To(Equal("medium"))
	})

	t.Run("When both need large it should return large", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := newCacheWithCPUAndMemory()
		// memory: 12Gi needs large, CPU: 10 needs large
		g.Expect(cache.recommendedSizeByBoth(12.0*1024*1024*1024, 10.0)).To(Equal("large"))
	})

	t.Run("When cache is empty it should return empty string", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		g.Expect(cache.recommendedSizeByBoth(1.0, 1.0)).To(Equal(""))
	})
}

func TestEffectiveMemoryFraction(t *testing.T) {
	t.Run("When no per-size override exists it should return the global fraction", func(t *testing.T) {
		g := NewGomegaWithT(t)
		globalFraction := resource.MustParse("0.7")
		cache := machineSizesCache{
			kasMemorySizeFraction: &globalFraction,
		}
		g.Expect(cache.effectiveMemoryFraction("small")).To(BeNumerically("~", 0.7, 0.0001))
	})

	t.Run("When no global fraction exists it should return the default", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		g.Expect(cache.effectiveMemoryFraction("small")).To(Equal(kubeAPIServerMemorySizeFractionDefault))
	})

	t.Run("When per-size override exists it should take precedence over global", func(t *testing.T) {
		g := NewGomegaWithT(t)
		globalFraction := resource.MustParse("0.7")
		perSizeFraction := resource.MustParse("0.55")
		cache := machineSizesCache{
			kasMemorySizeFraction: &globalFraction,
			perSizeFractions: map[string]sizeFractions{
				"small": {memoryFraction: &perSizeFraction},
			},
		}
		g.Expect(cache.effectiveMemoryFraction("small")).To(BeNumerically("~", 0.55, 0.0001))
		// medium has no per-size override, should use global
		g.Expect(cache.effectiveMemoryFraction("medium")).To(BeNumerically("~", 0.7, 0.0001))
	})
}

func TestEffectiveCPUFraction(t *testing.T) {
	t.Run("When no per-size override exists it should return the global CPU fraction", func(t *testing.T) {
		g := NewGomegaWithT(t)
		globalFraction := resource.MustParse("0.5")
		cache := machineSizesCache{
			kasCPUSizeFraction: &globalFraction,
		}
		g.Expect(cache.effectiveCPUFraction("small")).To(BeNumerically("~", 0.5, 0.0001))
	})

	t.Run("When no global CPU fraction exists it should return the default", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		g.Expect(cache.effectiveCPUFraction("small")).To(Equal(kubeAPIServerCPUSizeFractionDefault))
	})

	t.Run("When per-size override exists it should take precedence over global", func(t *testing.T) {
		g := NewGomegaWithT(t)
		globalFraction := resource.MustParse("0.5")
		perSizeFraction := resource.MustParse("0.4")
		cache := machineSizesCache{
			kasCPUSizeFraction: &globalFraction,
			perSizeFractions: map[string]sizeFractions{
				"small": {cpuFraction: &perSizeFraction},
			},
		}
		g.Expect(cache.effectiveCPUFraction("small")).To(BeNumerically("~", 0.4, 0.0001))
		// medium has no per-size override, should use global
		g.Expect(cache.effectiveCPUFraction("medium")).To(BeNumerically("~", 0.5, 0.0001))
	})
}

func TestPerSizeFractionValidation(t *testing.T) {
	log := stdr.New(nil)

	t.Run("When per-size memory fraction is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		invalidFraction := resource.MustParse("1.5")
		csc.Spec.Sizes[0].Capacity.KubeAPIServerMemoryFraction = &invalidFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerMemoryFraction for size"))
	})

	t.Run("When per-size CPU fraction is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		invalidFraction := resource.MustParse("1.5")
		csc.Spec.Sizes[0].Capacity.KubeAPIServerCPUFraction = &invalidFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("KubeAPIServerCPUFraction for size"))
	})

	t.Run("When per-size fractions are valid it should store them", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{}
		csc := defaultSizingConfigWithCapacity()
		memFraction := resource.MustParse("0.55")
		cpuFraction := resource.MustParse("0.4")
		csc.Spec.Sizes[0].Capacity.KubeAPIServerMemoryFraction = &memFraction
		csc.Spec.Sizes[0].Capacity.KubeAPIServerCPUFraction = &cpuFraction

		err := cache.update(csc, nil, log)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cache.effectiveMemoryFraction("small")).To(BeNumerically("~", 0.55, 0.0001))
		g.Expect(cache.effectiveCPUFraction("small")).To(BeNumerically("~", 0.4, 0.0001))
	})
}

func TestRecommendedSizeWithPerSizeFractions(t *testing.T) {
	t.Run("When per-size memory fraction is configured it should use it for sizing", func(t *testing.T) {
		g := NewGomegaWithT(t)
		// With small having a lower fraction, the effective capacity is reduced
		smallMemFraction := resource.MustParse("0.3")
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
			perSizeFractions: map[string]sizeFractions{
				"small": {memoryFraction: &smallMemFraction},
			},
		}
		// small effective capacity = 4Gi * 0.3 = 1.2Gi
		// Without per-size: 4Gi * 0.65 = 2.6Gi would fit 2Gi
		// With per-size: 4Gi * 0.3 = 1.2Gi does NOT fit 2Gi, so it should go to medium
		g.Expect(cache.recommendedSize(2.0 * 1024 * 1024 * 1024)).To(Equal("medium"))
	})
}

func TestRecommendedSizeByCPUWithPerSizeFractions(t *testing.T) {
	t.Run("When per-size CPU fraction is configured it should use it for CPU sizing", func(t *testing.T) {
		g := NewGomegaWithT(t)
		smallCPUFraction := resource.MustParse("0.25")
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi"), CPU: resource.MustParse("4")},
				"medium": {Memory: resource.MustParse("8Gi"), CPU: resource.MustParse("8")},
				"large":  {Memory: resource.MustParse("16Gi"), CPU: resource.MustParse("16")},
			},
			perSizeFractions: map[string]sizeFractions{
				"small": {cpuFraction: &smallCPUFraction},
			},
		}
		// small effective CPU capacity = 4 * 0.25 = 1.0
		// Without per-size: 4 * 0.65 = 2.6 would fit 2 CPU
		// With per-size: 4 * 0.25 = 1.0 does NOT fit 2 CPU, so it should go to medium
		g.Expect(cache.recommendedSizeByCPU(2.0)).To(Equal("medium"))
	})
}

func TestBackwardCompatNoCPUCapacity(t *testing.T) {
	t.Run("When no CPU capacity is configured it should still work for memory-only sizing", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cache := machineSizesCache{
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi")},
				"medium": {Memory: resource.MustParse("8Gi")},
				"large":  {Memory: resource.MustParse("16Gi")},
			},
		}
		// Memory-only sizing should work even with zero CPU
		g.Expect(cache.recommendedSize(2.0 * 1024 * 1024 * 1024)).To(Equal("small"))
		g.Expect(cache.recommendedSize(5.0 * 1024 * 1024 * 1024)).To(Equal("medium"))
	})
}
