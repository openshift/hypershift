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

package v1

import (
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PerformanceProfilePauseAnnotation allows an admin to suspend the operator's
// reconcile loop in order to perform manual changes to performance profile owned
// objects.
const PerformanceProfilePauseAnnotation = "performance.openshift.io/pause-reconcile"

// PerformanceProfileSpec defines the desired state of PerformanceProfile.
type PerformanceProfileSpec struct {
	// CPU defines a set of CPU related parameters.
	CPU *CPU `json:"cpu"`
	// HardwareTuning defines a set of CPU frequencies for isolated and reserved cpus.
	// It is an optional parameter and requires vendor recommendation to find suitable frequencies.
	// The intention is to set higher frequency for reserved cpus where
	// platform application is running while setting isolated cpus frequency to match
	// vendor recommendation.
	// +optional
	HardwareTuning *HardwareTuning `json:"hardwareTuning,omitempty"`
	// HugePages defines a set of huge pages related parameters.
	// It is possible to set huge pages with multiple size values at the same time.
	// For example, hugepages can be set with 1G and 2M, both values will be set on the node by the performance-addon-operator.
	// It is important to notice that setting hugepages default size to 1G will remove all 2M related
	// folders from the node and it will be impossible to configure 2M hugepages under the node.
	HugePages *HugePages `json:"hugepages,omitempty"`
	// MachineConfigLabel defines the label to add to the MachineConfigs the operator creates. It has to be
	// used in the MachineConfigSelector of the MachineConfigPool which targets this performance profile.
	// Defaults to "machineconfiguration.openshift.io/role=<same role as in NodeSelector label key>"
	// +optional
	MachineConfigLabel map[string]string `json:"machineConfigLabel,omitempty"`
	// MachineConfigPoolSelector defines the MachineConfigPool label to use in the MachineConfigPoolSelector
	// of resources like KubeletConfigs created by the operator.
	// Defaults to "machineconfiguration.openshift.io/role=<same role as in NodeSelector label key>"
	// +optional
	MachineConfigPoolSelector map[string]string `json:"machineConfigPoolSelector,omitempty"`
	// NodeSelector defines the Node label to use in the NodeSelectors of resources like Tuned created by the operator.
	// It most likely should, but does not have to match the node label in the NodeSelector of the MachineConfigPool
	// which targets this performance profile.
	// In the case when machineConfigLabels or machineConfigPoolSelector are not set, we are expecting a certain NodeSelector format
	// <domain>/<role>: "" in order to be able to calculate the default values for the former mentioned fields.
	NodeSelector map[string]string `json:"nodeSelector"`
	// RealTimeKernel defines a set of real time kernel related parameters. RT kernel won't be installed when not set.
	RealTimeKernel *RealTimeKernel `json:"realTimeKernel,omitempty"`
	// Additional kernel arguments.
	// +optional
	AdditionalKernelArgs []string `json:"additionalKernelArgs,omitempty"`
	// NUMA defines options related to topology aware affinities
	// +optional
	NUMA *NUMA `json:"numa,omitempty"`
	// Net defines a set of network related features
	// +optional
	Net *Net `json:"net,omitempty"`
	// GloballyDisableIrqLoadBalancing toggles whether IRQ load balancing will be disabled for the Isolated CPU set.
	// When the option is set to "true" it disables IRQs load balancing for the Isolated CPU set.
	// Setting the option to "false" allows the IRQs to be balanced across all CPUs, however the IRQs load balancing
	// can be disabled per pod CPUs when using irq-load-balancing.crio.io/cpu-quota.crio.io annotations.
	// Defaults to "false"
	// +optional
	GloballyDisableIrqLoadBalancing *bool `json:"globallyDisableIrqLoadBalancing,omitempty"`
	// WorkloadHints defines hints for different types of workloads. It will allow defining exact set of tuned and
	// kernel arguments that should be applied on top of the node.
	// +optional
	WorkloadHints *WorkloadHints `json:"workloadHints,omitempty"`
}

// CPUSet defines the set of CPUs(0-3,8-11).
type CPUSet string

// CPU defines a set of CPU related features.
type CPU struct {
	// Reserved defines a set of CPUs that will not be used for any container workloads initiated by kubelet.
	Reserved *CPUSet `json:"reserved,omitempty"`
	// Isolated defines a set of CPUs that will be used to give to application threads the most execution time possible,
	// which means removing as many extraneous tasks off a CPU as possible.
	// It is important to notice the CPU manager can choose any CPU to run the workload
	// except the reserved CPUs. In order to guarantee that your workload will run on the isolated CPU:
	//   1. The union of reserved CPUs and isolated CPUs should include all online CPUs
	//   2. The isolated CPUs field should be the complementary to reserved CPUs field
	Isolated *CPUSet `json:"isolated"`
	// BalanceIsolated toggles whether or not the Isolated CPU set is eligible for load balancing work loads.
	// When this option is set to "false", the Isolated CPU set will be static, meaning workloads have to
	// explicitly assign each thread to a specific cpu in order to work across multiple CPUs.
	// Setting this to "true" allows workloads to be balanced across CPUs.
	// Setting this to "false" offers the most predictable performance for guaranteed workloads, but it
	// offloads the complexity of cpu load balancing to the application.
	// Defaults to "true"
	// +optional
	BalanceIsolated *bool `json:"balanceIsolated,omitempty"`
	// Offline defines a set of CPUs that will be unused and set offline
	// +optional
	Offlined *CPUSet `json:"offlined,omitempty"`
}

// CPUfrequency defines cpu frequencies for isolated and reserved cpus
type CPUfrequency int

// HardwareTuning defines a set of CPU frequency related features.
type HardwareTuning struct {
	// IsolatedCpuFreq defines a minimum frequency to be set across isolated cpus
	IsolatedCpuFreq *CPUfrequency `json:"isolatedCpuFreq,omitempty"`
	// ReservedCpuFreq defines a maximum frequency to be set across reserved cpus
	ReservedCpuFreq *CPUfrequency `json:"reservedCpuFreq,omitempty"`
}

// HugePageSize defines size of huge pages, can be 2M or 1G.
type HugePageSize string

// HugePages defines a set of huge pages that we want to allocate at boot.
type HugePages struct {
	// DefaultHugePagesSize defines huge pages default size under kernel boot parameters.
	DefaultHugePagesSize *HugePageSize `json:"defaultHugepagesSize,omitempty"`
	// Pages defines huge pages that we want to allocate at boot time.
	Pages []HugePage `json:"pages,omitempty"`
}

// HugePage defines the number of allocated huge pages of the specific size.
type HugePage struct {
	// Size defines huge page size, maps to the 'hugepagesz' kernel boot parameter.
	Size HugePageSize `json:"size,omitempty"`
	// Count defines amount of huge pages, maps to the 'hugepages' kernel boot parameter.
	Count int32 `json:"count,omitempty"`
	// Node defines the NUMA node where hugepages will be allocated,
	// if not specified, pages will be allocated equally between NUMA nodes
	// +optional
	Node *int32 `json:"node,omitempty"`
}

// NUMA defines parameters related to topology awareness and affinity.
type NUMA struct {
	// Name of the policy applied when TopologyManager is enabled
	// Operator defaults to "best-effort"
	// +optional
	TopologyPolicy *string `json:"topologyPolicy,omitempty"`
}

// Net defines a set of network related features
type Net struct {
	// UserLevelNetworking when enabled - sets either all or specified network devices queue size to the amount of reserved CPUs. Defaults to "false".
	UserLevelNetworking *bool `json:"userLevelNetworking,omitempty"`
	// Devices contains a list of network device representations that will be
	// set with a netqueue count equal to CPU.Reserved .
	// If no devices are specified then the default is all devices.
	Devices []Device `json:"devices,omitempty"`
}

// Device defines a way to represent a network device in several options:
// device name, vendor ID, model ID, PCI path and MAC address
type Device struct {
	// Network device name to be matched. It uses a syntax of shell-style wildcards which are either positive or negative.
	// +optional
	InterfaceName *string `json:"interfaceName,omitempty"`
	// Network device vendor ID represnted as a 16 bit Hexmadecimal number.
	// +optional
	VendorID *string `json:"vendorID,omitempty"`
	// Network device ID (model) represnted as a 16 bit hexmadecimal number.
	// +optional
	DeviceID *string `json:"deviceID,omitempty"`
}

// RealTimeKernel defines the set of parameters relevant for the real time kernel.
type RealTimeKernel struct {
	// Enabled defines if the real time kernel packages should be installed. Defaults to "false"
	Enabled *bool `json:"enabled,omitempty"`
}

// WorkloadHints defines the set of upper level flags for different type of workloads.
type WorkloadHints struct {
	// HighPowerConsumption defines if the node should be configured in high power consumption mode.
	// The flag will affect the power consumption but will improve the CPUs latency.
	// +optional
	HighPowerConsumption *bool `json:"highPowerConsumption,omitempty"`
	// RealTime defines if the node should be configured for the real time workload. Defaults to true.
	// +default=true
	// +optional
	RealTime *bool `json:"realTime,omitempty"`
	// +optional
	// PerPodPowerManagement defines if the node should be configured in per pod power management.
	// PerPodPowerManagement and HighPowerConsumption hints can not be enabled together.
	PerPodPowerManagement *bool `json:"perPodPowerManagement,omitempty"`
}

// PerformanceProfileStatus defines the observed state of PerformanceProfile.
type PerformanceProfileStatus struct {
	// Conditions represents the latest available observations of current state.
	// +optional
	Conditions []conditionsv1.Condition `json:"conditions,omitempty"`
	// Tuned points to the Tuned custom resource object that contains the tuning values generated by this operator.
	// +optional
	Tuned *string `json:"tuned,omitempty"`
	// RuntimeClass contains the name of the RuntimeClass resource created by the operator.
	RuntimeClass *string `json:"runtimeClass,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=performanceprofiles,scope=Cluster
// +kubebuilder:deprecatedversion:warning="v1 is deprecated and should be removed in next three releases, use v2 instead"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerformanceProfile is the Schema for the performanceprofiles API
type PerformanceProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PerformanceProfileSpec   `json:"spec,omitempty"`
	Status PerformanceProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerformanceProfileList contains a list of PerformanceProfile
type PerformanceProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerformanceProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PerformanceProfile{}, &PerformanceProfileList{})
}
