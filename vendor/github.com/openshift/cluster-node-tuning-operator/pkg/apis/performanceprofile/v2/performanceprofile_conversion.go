package v2

import (
	"k8s.io/utils/ptr"

	v1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"

	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// ConvertTo converts this PerformanceProfile to the Hub version (v1).
func (curr *PerformanceProfile) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1.PerformanceProfile)

	// ObjectMeta
	dst.ObjectMeta = curr.ObjectMeta

	// Spec
	if curr.Spec.CPU != nil {
		dst.Spec.CPU = new(v1.CPU)

		if curr.Spec.CPU.Reserved != nil {
			reserved := v1.CPUSet(*curr.Spec.CPU.Reserved)
			dst.Spec.CPU.Reserved = &reserved
		}
		if curr.Spec.CPU.Isolated != nil {
			isolated := v1.CPUSet(*curr.Spec.CPU.Isolated)
			dst.Spec.CPU.Isolated = &isolated
		}
		if curr.Spec.CPU.BalanceIsolated != nil {
			dst.Spec.CPU.BalanceIsolated = ptr.To(*curr.Spec.CPU.BalanceIsolated)
		}
	}

	if curr.Spec.HardwareTuning != nil {
		dst.Spec.HardwareTuning = new(v1.HardwareTuning)
		if curr.Spec.HardwareTuning.IsolatedCpuFreq != nil {
			isolatedCpuFrequency := v1.CPUfrequency(*curr.Spec.HardwareTuning.IsolatedCpuFreq)
			dst.Spec.HardwareTuning.IsolatedCpuFreq = &isolatedCpuFrequency
		}
		if curr.Spec.HardwareTuning.ReservedCpuFreq != nil {
			reservedCpuFrequency := v1.CPUfrequency(*curr.Spec.HardwareTuning.ReservedCpuFreq)
			dst.Spec.HardwareTuning.ReservedCpuFreq = &reservedCpuFrequency
		}
	}
	if curr.Spec.HugePages != nil {
		dst.Spec.HugePages = new(v1.HugePages)

		if curr.Spec.HugePages.DefaultHugePagesSize != nil {
			defaultHugePagesSize := v1.HugePageSize(*curr.Spec.HugePages.DefaultHugePagesSize)
			dst.Spec.HugePages.DefaultHugePagesSize = &defaultHugePagesSize
		}

		if curr.Spec.HugePages.Pages != nil {
			dst.Spec.HugePages.Pages = make([]v1.HugePage, len(curr.Spec.HugePages.Pages))

			for i, p := range curr.Spec.HugePages.Pages {
				dst.Spec.HugePages.Pages[i] = v1.HugePage{
					Size: v1.HugePageSize(p.Size), Count: p.Count,
				}
				if p.Node != nil {
					dst.Spec.HugePages.Pages[i].Node = ptr.To[int32](*p.Node)
				}
			}
		}
	}

	if curr.Spec.MachineConfigLabel != nil {
		dst.Spec.MachineConfigLabel = make(map[string]string)
		for k, v := range curr.Spec.MachineConfigLabel {
			dst.Spec.MachineConfigLabel[k] = v
		}
	}

	if curr.Spec.MachineConfigPoolSelector != nil {
		dst.Spec.MachineConfigPoolSelector = make(map[string]string)
		for k, v := range curr.Spec.MachineConfigPoolSelector {
			dst.Spec.MachineConfigPoolSelector[k] = v
		}
	}

	if curr.Spec.NodeSelector != nil {
		dst.Spec.NodeSelector = make(map[string]string)
		for k, v := range curr.Spec.NodeSelector {
			dst.Spec.NodeSelector[k] = v
		}
	}

	if curr.Spec.RealTimeKernel != nil {
		dst.Spec.RealTimeKernel = new(v1.RealTimeKernel)

		if curr.Spec.RealTimeKernel.Enabled != nil {
			dst.Spec.RealTimeKernel.Enabled = ptr.To(*curr.Spec.RealTimeKernel.Enabled)
		}
	}

	if curr.Spec.AdditionalKernelArgs != nil {
		dst.Spec.AdditionalKernelArgs = make([]string, len(curr.Spec.AdditionalKernelArgs))
		copy(dst.Spec.AdditionalKernelArgs, curr.Spec.AdditionalKernelArgs)
	}

	if curr.Spec.NUMA != nil {
		dst.Spec.NUMA = new(v1.NUMA)

		if curr.Spec.NUMA.TopologyPolicy != nil {
			dst.Spec.NUMA.TopologyPolicy = ptr.To[string](*curr.Spec.NUMA.TopologyPolicy)
		}
	}

	// Convert Net fields
	if curr.Spec.Net != nil {
		dst.Spec.Net = new(v1.Net)

		if curr.Spec.Net.UserLevelNetworking != nil {
			dst.Spec.Net.UserLevelNetworking = ptr.To(*curr.Spec.Net.UserLevelNetworking)
		}

		if curr.Spec.Net.Devices != nil {
			dst.Spec.Net.Devices = []v1.Device{}

			for _, d := range curr.Spec.Net.Devices {
				device := v1.Device{}

				if d.VendorID != nil {
					device.VendorID = ptr.To[string](*d.VendorID)
				}

				if d.DeviceID != nil {
					device.DeviceID = ptr.To[string](*d.DeviceID)
				}

				if d.InterfaceName != nil {
					device.InterfaceName = ptr.To[string](*d.InterfaceName)
				}

				dst.Spec.Net.Devices = append(dst.Spec.Net.Devices, device)
			}
		}
	}

	if curr.Spec.GloballyDisableIrqLoadBalancing != nil {
		dst.Spec.GloballyDisableIrqLoadBalancing = ptr.To(*curr.Spec.GloballyDisableIrqLoadBalancing)
	}

	// Status
	if curr.Status.Conditions != nil {
		dst.Status.Conditions = make([]conditionsv1.Condition, len(curr.Status.Conditions))
		copy(dst.Status.Conditions, curr.Status.Conditions)
	}

	if curr.Status.Tuned != nil {
		dst.Status.Tuned = ptr.To[string](*curr.Status.Tuned)
	}

	if curr.Status.RuntimeClass != nil {
		dst.Status.RuntimeClass = ptr.To[string](*curr.Status.RuntimeClass)
	}

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}

// ConvertFrom converts from the Hub version (v1) to this version.
func (curr *PerformanceProfile) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1.PerformanceProfile)

	// ObjectMeta
	curr.ObjectMeta = src.ObjectMeta

	// Spec
	if src.Spec.CPU != nil {
		curr.Spec.CPU = new(CPU)

		if src.Spec.CPU.Reserved != nil {
			reserved := CPUSet(*src.Spec.CPU.Reserved)
			curr.Spec.CPU.Reserved = &reserved
		}
		if src.Spec.CPU.Isolated != nil {
			isolated := CPUSet(*src.Spec.CPU.Isolated)
			curr.Spec.CPU.Isolated = &isolated
		}
		if src.Spec.CPU.BalanceIsolated != nil {
			curr.Spec.CPU.BalanceIsolated = ptr.To(*src.Spec.CPU.BalanceIsolated)
		}
	}

	if src.Spec.HugePages != nil {
		curr.Spec.HugePages = new(HugePages)

		if src.Spec.HugePages.DefaultHugePagesSize != nil {
			defaultHugePagesSize := HugePageSize(*src.Spec.HugePages.DefaultHugePagesSize)
			curr.Spec.HugePages.DefaultHugePagesSize = &defaultHugePagesSize
		}

		if src.Spec.HugePages.Pages != nil {
			curr.Spec.HugePages.Pages = make([]HugePage, len(src.Spec.HugePages.Pages))
			for i, p := range src.Spec.HugePages.Pages {
				curr.Spec.HugePages.Pages[i] = HugePage{
					Size: HugePageSize(p.Size), Count: p.Count,
				}
				if p.Node != nil {
					curr.Spec.HugePages.Pages[i].Node = ptr.To[int32](*p.Node)
				}
			}
		}
	}

	if src.Spec.MachineConfigLabel != nil {
		curr.Spec.MachineConfigLabel = make(map[string]string)
		for k, v := range src.Spec.MachineConfigLabel {
			curr.Spec.MachineConfigLabel[k] = v
		}
	}

	if src.Spec.MachineConfigPoolSelector != nil {
		curr.Spec.MachineConfigPoolSelector = make(map[string]string)
		for k, v := range src.Spec.MachineConfigPoolSelector {
			curr.Spec.MachineConfigPoolSelector[k] = v
		}
	}

	if src.Spec.NodeSelector != nil {
		curr.Spec.NodeSelector = make(map[string]string)
		for k, v := range src.Spec.NodeSelector {
			curr.Spec.NodeSelector[k] = v
		}
	}

	if src.Spec.RealTimeKernel != nil {
		curr.Spec.RealTimeKernel = new(RealTimeKernel)

		if src.Spec.RealTimeKernel.Enabled != nil {
			curr.Spec.RealTimeKernel.Enabled = ptr.To(*src.Spec.RealTimeKernel.Enabled)
		}
	}

	if src.Spec.AdditionalKernelArgs != nil {
		curr.Spec.AdditionalKernelArgs = make([]string, len(src.Spec.AdditionalKernelArgs))
		copy(curr.Spec.AdditionalKernelArgs, src.Spec.AdditionalKernelArgs)
	}

	if src.Spec.NUMA != nil {
		curr.Spec.NUMA = new(NUMA)

		if src.Spec.NUMA.TopologyPolicy != nil {
			curr.Spec.NUMA.TopologyPolicy = ptr.To[string](*src.Spec.NUMA.TopologyPolicy)
		}
	}

	// Convert Net fields
	if src.Spec.Net != nil {
		curr.Spec.Net = new(Net)

		if src.Spec.Net.UserLevelNetworking != nil {
			curr.Spec.Net.UserLevelNetworking = ptr.To(*src.Spec.Net.UserLevelNetworking)
		}

		if src.Spec.Net.Devices != nil {
			curr.Spec.Net.Devices = []Device{}

			for _, d := range src.Spec.Net.Devices {
				device := Device{}

				if d.VendorID != nil {
					device.VendorID = ptr.To[string](*d.VendorID)
				}

				if d.DeviceID != nil {
					device.DeviceID = ptr.To[string](*d.DeviceID)
				}

				if d.InterfaceName != nil {
					device.InterfaceName = ptr.To[string](*d.InterfaceName)
				}

				curr.Spec.Net.Devices = append(curr.Spec.Net.Devices, device)
			}
		}
	}

	if src.Spec.GloballyDisableIrqLoadBalancing != nil {
		curr.Spec.GloballyDisableIrqLoadBalancing = ptr.To(*src.Spec.GloballyDisableIrqLoadBalancing)
	} else { // set to true by default
		curr.Spec.GloballyDisableIrqLoadBalancing = ptr.To(true)
	}

	// Status
	if src.Status.Conditions != nil {
		curr.Status.Conditions = make([]conditionsv1.Condition, len(src.Status.Conditions))
		copy(curr.Status.Conditions, src.Status.Conditions)
	}

	if src.Status.Tuned != nil {
		curr.Status.Tuned = ptr.To[string](*src.Status.Tuned)
	}

	if src.Status.RuntimeClass != nil {
		curr.Status.RuntimeClass = ptr.To[string](*src.Status.RuntimeClass)
	}

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}
