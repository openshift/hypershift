/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

*/

package v2

import (
	"context"
	"fmt"
	"reflect"
	"regexp"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

const (
	hugepagesSize2M = "2M"
	hugepagesSize1G = "1G"
)

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateCreate() error {
	klog.Infof("Create validation for the performance profile %q", r.Name)

	return r.validateCreateOrUpdate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateUpdate(old runtime.Object) error {
	klog.Infof("Update validation for the performance profile %q", r.Name)

	return r.validateCreateOrUpdate()
}

func (r *PerformanceProfile) validateCreateOrUpdate() error {
	var allErrs field.ErrorList

	// validate node selector duplication
	ppList := &PerformanceProfileList{}
	if err := validatorClient.List(context.TODO(), ppList); err != nil {
		return apierrors.NewInternalError(err)
	}

	allErrs = append(allErrs, r.validateNodeSelectorDuplication(ppList)...)

	// validate basic fields
	allErrs = append(allErrs, r.validateFields()...)

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "performance.openshift.io", Kind: "PerformanceProfile"},
		r.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *PerformanceProfile) ValidateDelete() error {
	klog.Infof("Delete validation for the performance profile %q", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}

func (r *PerformanceProfile) validateNodeSelectorDuplication(ppList *PerformanceProfileList) field.ErrorList {
	var allErrs field.ErrorList

	// validate node selector duplication
	for _, pp := range ppList.Items {
		// exclude the current profile from the check
		if pp.Name == r.Name {
			continue
		}

		if reflect.DeepEqual(pp.Spec.NodeSelector, r.Spec.NodeSelector) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.nodeSelector"), r.Spec.NodeSelector, fmt.Sprintf("the profile has the same node selector as the performance profile %q", pp.Name)))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateFields() field.ErrorList {
	var allErrs field.ErrorList

	allErrs = append(allErrs, r.validateCPUs()...)
	allErrs = append(allErrs, r.validateSelectors()...)
	allErrs = append(allErrs, r.validateHugePages()...)
	allErrs = append(allErrs, r.validateNUMA()...)
	allErrs = append(allErrs, r.validateNet()...)

	return allErrs
}

func (r *PerformanceProfile) validateCPUs() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.CPU == nil {
		allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu"), "cpu section required"))
	} else {
		if r.Spec.CPU.Isolated == nil {
			allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu.isolated"), "isolated CPUs required"))
		}

		if r.Spec.CPU.Reserved == nil {
			allErrs = append(allErrs, field.Required(field.NewPath("spec.cpu.reserved"), "reserved CPUs required"))
		}

		if r.Spec.CPU.Isolated != nil && r.Spec.CPU.Reserved != nil {
			var offlined string
			if r.Spec.CPU.Offlined != nil {
				offlined = string(*r.Spec.CPU.Offlined)
			}

			cpuLists, err := components.NewCPULists(string(*r.Spec.CPU.Reserved), string(*r.Spec.CPU.Isolated), offlined)
			if err != nil {
				allErrs = append(allErrs, field.InternalError(field.NewPath("spec.cpu"), err))
			}

			if cpuLists != nil {
				if cpuLists.GetReserved().IsEmpty() {
					allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu.reserved"), r.Spec.CPU.Reserved, "reserved CPUs can not be empty"))
				}

				if cpuLists.GetIsolated().IsEmpty() {
					allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu.isolated"), r.Spec.CPU.Isolated, "isolated CPUs can not be empty"))
				}

				if overlap := components.Intersect(cpuLists.GetIsolated(), cpuLists.GetReserved()); len(overlap) != 0 {
					allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu"), r.Spec.CPU, fmt.Sprintf("reserved and isolated cpus overlap: %v", overlap)))
				}
			}

			if r.Spec.CPU.Offlined != nil {
				if overlap := components.Intersect(cpuLists.GetReserved(), cpuLists.GetOfflined()); len(overlap) != 0 {
					allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu"), r.Spec.CPU, fmt.Sprintf("reserved and offlined cpus overlap: %v", overlap)))
				}
				if overlap := components.Intersect(cpuLists.GetIsolated(), cpuLists.GetOfflined()); len(overlap) != 0 {
					allErrs = append(allErrs, field.Invalid(field.NewPath("spec.cpu"), r.Spec.CPU, fmt.Sprintf("isolated and offlined cpus overlap: %v", overlap)))
				}
			}
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateSelectors() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.MachineConfigLabel != nil && len(r.Spec.MachineConfigLabel) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.machineConfigLabel"), r.Spec.MachineConfigLabel, "you should provide only 1 MachineConfigLabel"))
	}

	if r.Spec.MachineConfigPoolSelector != nil && len(r.Spec.MachineConfigPoolSelector) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.machineConfigPoolSelector"), r.Spec.MachineConfigLabel, "you should provide only 1 MachineConfigPoolSelector"))
	}

	if r.Spec.NodeSelector == nil {
		allErrs = append(allErrs, field.Required(field.NewPath("spec.nodeSelector"), "the nodeSelector required"))
	}

	if len(r.Spec.NodeSelector) > 1 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.nodeSelector"), r.Spec.NodeSelector, "you should provide ony 1 NodeSelector"))
	}

	// in case MachineConfigLabels or MachineConfigPoolSelector are not set, we expect a certain format (domain/role)
	// on the NodeSelector in order to be able to calculate the default values for the former metioned fields.
	if r.Spec.MachineConfigLabel == nil || r.Spec.MachineConfigPoolSelector == nil {
		k, _ := components.GetFirstKeyAndValue(r.Spec.NodeSelector)
		if _, _, err := components.SplitLabelKey(k); err != nil {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.nodeSelector"),
				r.Spec.NodeSelector,
				"machineConfigLabels or machineConfigPoolSelector are not set, but we  can not set it automatically because of an invalid NodeSelector label key that can't be split into domain/role"))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateHugePages() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.HugePages == nil {
		return allErrs
	}

	// validate that default hugepages size has correct value, currently we support only 2M and 1G(x86_64 architecture)
	if r.Spec.HugePages.DefaultHugePagesSize != nil {
		defaultSize := *r.Spec.HugePages.DefaultHugePagesSize
		if defaultSize != hugepagesSize1G && defaultSize != hugepagesSize2M {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.defaultHugepagesSize"), r.Spec.HugePages.DefaultHugePagesSize, fmt.Sprintf("hugepages default size should be equal to %q or %q", hugepagesSize1G, hugepagesSize2M)))
		}
	}

	for i, page := range r.Spec.HugePages.Pages {
		if page.Size != hugepagesSize1G && page.Size != hugepagesSize2M {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.pages"), r.Spec.HugePages.Pages, fmt.Sprintf("the page size should be equal to %q or %q", hugepagesSize1G, hugepagesSize2M)))
		}

		allErrs = append(allErrs, r.validatePageDuplication(&page, r.Spec.HugePages.Pages[i+1:])...)
	}

	return allErrs
}

func (r *PerformanceProfile) validatePageDuplication(page *HugePage, pages []HugePage) field.ErrorList {
	var allErrs field.ErrorList

	for _, p := range pages {
		if page.Size != p.Size {
			continue
		}

		if page.Node != nil && p.Node == nil {
			continue
		}

		if page.Node == nil && p.Node != nil {
			continue
		}

		if page.Node == nil && p.Node == nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.pages"), r.Spec.HugePages.Pages, fmt.Sprintf("the page with the size %q and without the specified NUMA node, has duplication", page.Size)))
			continue
		}

		if *page.Node == *p.Node {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.hugepages.pages"), r.Spec.HugePages.Pages, fmt.Sprintf("the page with the size %q and with specified NUMA node %d, has duplication", page.Size, *page.Node)))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateNUMA() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.NUMA == nil {
		return allErrs
	}

	// validate NUMA topology policy matches allowed values
	if r.Spec.NUMA.TopologyPolicy != nil {
		policy := *r.Spec.NUMA.TopologyPolicy
		if policy != kubeletconfigv1beta1.NoneTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.BestEffortTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.RestrictedTopologyManagerPolicy &&
			policy != kubeletconfigv1beta1.SingleNumaNodeTopologyManagerPolicy {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.numa.topologyPolicy"), r.Spec.NUMA.TopologyPolicy, "unrecognized value for topologyPolicy"))
		}
	}

	return allErrs
}

func (r *PerformanceProfile) validateNet() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.Net == nil {
		return allErrs
	}

	if r.Spec.Net.UserLevelNetworking != nil && *r.Spec.Net.UserLevelNetworking && r.Spec.CPU.Reserved == nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net"), r.Spec.Net, "can not set network devices queues count without specifiying spec.cpu.reserved"))
	}

	for _, device := range r.Spec.Net.Devices {
		if device.InterfaceName != nil && *device.InterfaceName == "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, "device name cannot be empty"))
		}
		if device.VendorID != nil && !isValid16bitsHexID(*device.VendorID) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, fmt.Sprintf("device vendor ID %s has an invalid format. Vendor ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", *device.VendorID)))
		}
		if device.DeviceID != nil && !isValid16bitsHexID(*device.DeviceID) {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, fmt.Sprintf("device model ID %s has an invalid format. Model ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", *device.DeviceID)))
		}
		if device.DeviceID != nil && device.VendorID == nil {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec.net.devices"), r.Spec.Net.Devices, fmt.Sprintf("device model ID can not be used without specifying the device vendor ID.")))
		}
	}
	return allErrs
}

func isValid16bitsHexID(v string) bool {
	re := regexp.MustCompile("^0x[0-9a-fA-F]+$")
	return re.MatchString(v) && len(v) < 7
}

func (r *PerformanceProfile) validateWorkloadHints() field.ErrorList {
	var allErrs field.ErrorList

	if r.Spec.WorkloadHints == nil {
		return allErrs
	}

	if r.Spec.RealTimeKernel != nil {
		if r.Spec.RealTimeKernel.Enabled != nil && *r.Spec.RealTimeKernel.Enabled {
			if r.Spec.WorkloadHints.RealTime != nil && !*r.Spec.WorkloadHints.RealTime {
				allErrs = append(allErrs, field.Invalid(field.NewPath("spec.workloadHints.realTime"), r.Spec.WorkloadHints.RealTime, "realtime kernel is enabled, but realtime workload hint is explicitly disable"))
			}
		}
	}

	return allErrs
}
