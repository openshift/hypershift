/*
Copyright 2023 The Kubernetes Authors.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

const (
	// LabelAgentPoolMode represents mode of an agent pool. Possible values include: System, User.
	LabelAgentPoolMode = "azuremanagedmachinepool.infrastructure.cluster.x-k8s.io/agentpoolmode"

	// NodePoolModeSystem represents mode system for azuremachinepool.
	NodePoolModeSystem NodePoolMode = "System"

	// NodePoolModeUser represents mode user for azuremachinepool.
	NodePoolModeUser NodePoolMode = "User"

	// DefaultOSType represents the default operating system for azmachinepool.
	DefaultOSType string = LinuxOS
)

// NodePoolMode enumerates the values for agent pool mode.
type NodePoolMode string

// CPUManagerPolicy enumerates the values for KubeletConfig.CPUManagerPolicy.
type CPUManagerPolicy string

const (
	// CPUManagerPolicyNone ...
	CPUManagerPolicyNone CPUManagerPolicy = "none"
	// CPUManagerPolicyStatic ...
	CPUManagerPolicyStatic CPUManagerPolicy = "static"
)

// TopologyManagerPolicy enumerates the values for KubeletConfig.TopologyManagerPolicy.
type TopologyManagerPolicy string

// KubeletDiskType enumerates the values for the agent pool's KubeletDiskType.
type KubeletDiskType string

const (
	// KubeletDiskTypeOS ...
	KubeletDiskTypeOS KubeletDiskType = "OS"
	// KubeletDiskTypeTemporary ...
	KubeletDiskTypeTemporary KubeletDiskType = "Temporary"
)

const (
	// TopologyManagerPolicyNone ...
	TopologyManagerPolicyNone TopologyManagerPolicy = "none"
	// TopologyManagerPolicyBestEffort ...
	TopologyManagerPolicyBestEffort TopologyManagerPolicy = "best-effort"
	// TopologyManagerPolicyRestricted ...
	TopologyManagerPolicyRestricted TopologyManagerPolicy = "restricted"
	// TopologyManagerPolicySingleNumaNode ...
	TopologyManagerPolicySingleNumaNode TopologyManagerPolicy = "single-numa-node"
)

// TransparentHugePageOption enumerates the values for various modes of Transparent Hugepages.
type TransparentHugePageOption string

const (
	// TransparentHugePageOptionAlways ...
	TransparentHugePageOptionAlways TransparentHugePageOption = "always"

	// TransparentHugePageOptionMadvise ...
	TransparentHugePageOptionMadvise TransparentHugePageOption = "madvise"

	// TransparentHugePageOptionNever ...
	TransparentHugePageOptionNever TransparentHugePageOption = "never"

	// TransparentHugePageOptionDefer ...
	TransparentHugePageOptionDefer TransparentHugePageOption = "defer"

	// TransparentHugePageOptionDeferMadvise ...
	TransparentHugePageOptionDeferMadvise TransparentHugePageOption = "defer+madvise"
)

// KubeletConfig defines the set of kubelet configurations for nodes in pools.
type KubeletConfig struct {
	// CPUManagerPolicy - CPU Manager policy to use.
	// +kubebuilder:validation:Enum=none;static
	// +optional
	CPUManagerPolicy *CPUManagerPolicy `json:"cpuManagerPolicy,omitempty"`
	// CPUCfsQuota - Enable CPU CFS quota enforcement for containers that specify CPU limits.
	// +optional
	CPUCfsQuota *bool `json:"cpuCfsQuota,omitempty"`
	// CPUCfsQuotaPeriod - Sets CPU CFS quota period value.
	// +optional
	CPUCfsQuotaPeriod *string `json:"cpuCfsQuotaPeriod,omitempty"`
	// ImageGcHighThreshold - The percent of disk usage after which image garbage collection is always run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGcHighThreshold *int32 `json:"imageGcHighThreshold,omitempty"`
	// ImageGcLowThreshold - The percent of disk usage before which image garbage collection is never run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGcLowThreshold *int32 `json:"imageGcLowThreshold,omitempty"`
	// TopologyManagerPolicy - Topology Manager policy to use.
	// +kubebuilder:validation:Enum=none;best-effort;restricted;single-numa-node
	// +optional
	TopologyManagerPolicy *TopologyManagerPolicy `json:"topologyManagerPolicy,omitempty"`
	// AllowedUnsafeSysctls - Allowlist of unsafe sysctls or unsafe sysctl patterns (ending in `*`).
	// +optional
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`
	// FailSwapOn - If set to true it will make the Kubelet fail to start if swap is enabled on the node.
	// +optional
	FailSwapOn *bool `json:"failSwapOn,omitempty"`
	// ContainerLogMaxSizeMB - The maximum size (e.g. 10Mi) of container log file before it is rotated.
	// +optional
	ContainerLogMaxSizeMB *int32 `json:"containerLogMaxSizeMB,omitempty"`
	// ContainerLogMaxFiles - The maximum number of container log files that can be present for a container. The number must be ≥ 2.
	// +kubebuilder:validation:Minimum=2
	// +optional
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`
	// PodMaxPids - The maximum number of processes per pod.
	// +kubebuilder:validation:Minimum=-1
	// +optional
	PodMaxPids *int32 `json:"podMaxPids,omitempty"`
}

// SysctlConfig specifies the settings for Linux agent nodes.
type SysctlConfig struct {
	// FsAioMaxNr specifies the maximum number of system-wide asynchronous io requests.
	// Maps to fs.aio-max-nr.
	// +kubebuilder:validation:Minimum=65536
	// +kubebuilder:validation:Maximum=6553500
	// +optional
	FsAioMaxNr *int32 `json:"fsAioMaxNr,omitempty"`

	// FsFileMax specifies the max number of file-handles that the Linux kernel will allocate, by increasing increases the maximum number of open files permitted.
	// Maps to fs.file-max.
	// +kubebuilder:validation:Minimum=8192
	// +kubebuilder:validation:Maximum=12000500
	// +optional
	FsFileMax *int32 `json:"fsFileMax,omitempty"`

	// FsInotifyMaxUserWatches specifies the number of file watches allowed by the system. Each watch is roughly 90 bytes on a 32-bit kernel, and roughly 160 bytes on a 64-bit kernel.
	// Maps to fs.inotify.max_user_watches.
	// +kubebuilder:validation:Minimum=781250
	// +kubebuilder:validation:Maximum=2097152
	// +optional
	FsInotifyMaxUserWatches *int32 `json:"fsInotifyMaxUserWatches,omitempty"`

	// FsNrOpen specifies the maximum number of file-handles a process can allocate.
	// Maps to fs.nr_open.
	// +kubebuilder:validation:Minimum=8192
	// +kubebuilder:validation:Maximum=20000500
	// +optional
	FsNrOpen *int32 `json:"fsNrOpen,omitempty"`

	// KernelThreadsMax specifies the maximum number of all threads that can be created.
	// Maps to kernel.threads-max.
	// +kubebuilder:validation:Minimum=20
	// +kubebuilder:validation:Maximum=513785
	// +optional
	KernelThreadsMax *int32 `json:"kernelThreadsMax,omitempty"`

	// NetCoreNetdevMaxBacklog specifies maximum number of packets, queued on the INPUT side, when the interface receives packets faster than kernel can process them.
	// Maps to net.core.netdev_max_backlog.
	// +kubebuilder:validation:Minimum=1000
	// +kubebuilder:validation:Maximum=3240000
	// +optional
	NetCoreNetdevMaxBacklog *int32 `json:"netCoreNetdevMaxBacklog,omitempty"`

	// NetCoreOptmemMax specifies the maximum ancillary buffer size (option memory buffer) allowed per socket.
	// Socket option memory is used in a few cases to store extra structures relating to usage of the socket.
	// Maps to net.core.optmem_max.
	// +kubebuilder:validation:Minimum=20480
	// +kubebuilder:validation:Maximum=4194304
	// +optional
	NetCoreOptmemMax *int32 `json:"netCoreOptmemMax,omitempty"`

	// NetCoreRmemDefault specifies the default receive socket buffer size in bytes.
	// Maps to net.core.rmem_default.
	// +kubebuilder:validation:Minimum=212992
	// +kubebuilder:validation:Maximum=134217728
	// +optional
	NetCoreRmemDefault *int32 `json:"netCoreRmemDefault,omitempty"`

	// NetCoreRmemMax specifies the maximum receive socket buffer size in bytes.
	// Maps to net.core.rmem_max.
	// +kubebuilder:validation:Minimum=212992
	// +kubebuilder:validation:Maximum=134217728
	// +optional
	NetCoreRmemMax *int32 `json:"netCoreRmemMax,omitempty"`

	// NetCoreSomaxconn specifies maximum number of connection requests that can be queued for any given listening socket.
	// An upper limit for the value of the backlog parameter passed to the listen(2)(https://man7.org/linux/man-pages/man2/listen.2.html) function.
	// If the backlog argument is greater than the somaxconn, then it's silently truncated to this limit.
	// Maps to net.core.somaxconn.
	// +kubebuilder:validation:Minimum=4096
	// +kubebuilder:validation:Maximum=3240000
	// +optional
	NetCoreSomaxconn *int32 `json:"netCoreSomaxconn,omitempty"`

	// NetCoreWmemDefault specifies the default send socket buffer size in bytes.
	// Maps to net.core.wmem_default.
	// +kubebuilder:validation:Minimum=212992
	// +kubebuilder:validation:Maximum=134217728
	// +optional
	NetCoreWmemDefault *int32 `json:"netCoreWmemDefault,omitempty"`

	// NetCoreWmemMax specifies the maximum send socket buffer size in bytes.
	// Maps to net.core.wmem_max.
	// +kubebuilder:validation:Minimum=212992
	// +kubebuilder:validation:Maximum=134217728
	// +optional
	NetCoreWmemMax *int32 `json:"netCoreWmemMax,omitempty"`

	// NetIpv4IPLocalPortRange is used by TCP and UDP traffic to choose the local port on the agent node.
	// PortRange should be specified in the format "first last".
	// First, being an integer, must be between [1024 - 60999].
	// Last, being an integer, must be between [32768 - 65000].
	// Maps to net.ipv4.ip_local_port_range.
	// +optional
	NetIpv4IPLocalPortRange *string `json:"netIpv4IPLocalPortRange,omitempty"`

	// NetIpv4NeighDefaultGcThresh1 specifies the minimum number of entries that may be in the ARP cache.
	// Garbage collection won't be triggered if the number of entries is below this setting.
	// Maps to net.ipv4.neigh.default.gc_thresh1.
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=80000
	// +optional
	NetIpv4NeighDefaultGcThresh1 *int32 `json:"netIpv4NeighDefaultGcThresh1,omitempty"`

	// NetIpv4NeighDefaultGcThresh2 specifies soft maximum number of entries that may be in the ARP cache.
	// ARP garbage collection will be triggered about 5 seconds after reaching this soft maximum.
	// Maps to net.ipv4.neigh.default.gc_thresh2.
	// +kubebuilder:validation:Minimum=512
	// +kubebuilder:validation:Maximum=90000
	// +optional
	NetIpv4NeighDefaultGcThresh2 *int32 `json:"netIpv4NeighDefaultGcThresh2,omitempty"`

	// NetIpv4NeighDefaultGcThresh3 specified hard maximum number of entries in the ARP cache.
	// Maps to net.ipv4.neigh.default.gc_thresh3.
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=100000
	// +optional
	NetIpv4NeighDefaultGcThresh3 *int32 `json:"netIpv4NeighDefaultGcThresh3,omitempty"`

	// NetIpv4TCPFinTimeout specifies the length of time an orphaned connection will remain in the FIN_WAIT_2 state before it's aborted at the local end.
	// Maps to net.ipv4.tcp_fin_timeout.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=120
	// +optional
	NetIpv4TCPFinTimeout *int32 `json:"netIpv4TCPFinTimeout,omitempty"`

	// NetIpv4TCPKeepaliveProbes specifies the number of keepalive probes TCP sends out, until it decides the connection is broken.
	// Maps to net.ipv4.tcp_keepalive_probes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=15
	// +optional
	NetIpv4TCPKeepaliveProbes *int32 `json:"netIpv4TCPKeepaliveProbes,omitempty"`

	// NetIpv4TCPKeepaliveTime specifies the rate at which TCP sends out a keepalive message when keepalive is enabled.
	// Maps to net.ipv4.tcp_keepalive_time.
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=432000
	// +optional
	NetIpv4TCPKeepaliveTime *int32 `json:"netIpv4TCPKeepaliveTime,omitempty"`

	// NetIpv4TCPMaxSynBacklog specifies the maximum number of queued connection requests that have still not received an acknowledgment from the connecting client.
	// If this number is exceeded, the kernel will begin dropping requests.
	// Maps to net.ipv4.tcp_max_syn_backlog.
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=3240000
	// +optional
	NetIpv4TCPMaxSynBacklog *int32 `json:"netIpv4TCPMaxSynBacklog,omitempty"`

	// NetIpv4TCPMaxTwBuckets specifies maximal number of timewait sockets held by system simultaneously.
	// If this number is exceeded, time-wait socket is immediately destroyed and warning is printed.
	// Maps to net.ipv4.tcp_max_tw_buckets.
	// +kubebuilder:validation:Minimum=8000
	// +kubebuilder:validation:Maximum=1440000
	// +optional
	NetIpv4TCPMaxTwBuckets *int32 `json:"netIpv4TCPMaxTwBuckets,omitempty"`

	// NetIpv4TCPTwReuse is used to allow to reuse TIME-WAIT sockets for new connections when it's safe from protocol viewpoint.
	// Maps to net.ipv4.tcp_tw_reuse.
	// +optional
	NetIpv4TCPTwReuse *bool `json:"netIpv4TCPTwReuse,omitempty"`

	// NetIpv4TCPkeepaliveIntvl specifies the frequency of the probes sent out.
	// Multiplied by tcpKeepaliveprobes, it makes up the time to kill a connection that isn't responding, after probes started.
	// Maps to net.ipv4.tcp_keepalive_intvl.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=75
	// +optional
	NetIpv4TCPkeepaliveIntvl *int32 `json:"netIpv4TCPkeepaliveIntvl,omitempty"`

	// NetNetfilterNfConntrackBuckets specifies the size of hash table used by nf_conntrack module to record the established connection record of the TCP protocol.
	// Maps to net.netfilter.nf_conntrack_buckets.
	// +kubebuilder:validation:Minimum=65536
	// +kubebuilder:validation:Maximum=147456
	// +optional
	NetNetfilterNfConntrackBuckets *int32 `json:"netNetfilterNfConntrackBuckets,omitempty"`

	// NetNetfilterNfConntrackMax specifies the maximum number of connections supported by the nf_conntrack module or the size of connection tracking table.
	// Maps to net.netfilter.nf_conntrack_max.
	// +kubebuilder:validation:Minimum=131072
	// +kubebuilder:validation:Maximum=1048576
	// +optional
	NetNetfilterNfConntrackMax *int32 `json:"netNetfilterNfConntrackMax,omitempty"`

	// VMMaxMapCount specifies the maximum number of memory map areas a process may have.
	// Maps to vm.max_map_count.
	// +kubebuilder:validation:Minimum=65530
	// +kubebuilder:validation:Maximum=262144
	// +optional
	VMMaxMapCount *int32 `json:"vmMaxMapCount,omitempty"`

	// VMSwappiness specifies aggressiveness of the kernel in swapping memory pages.
	// Higher values will increase aggressiveness, lower values decrease the amount of swap.
	// Maps to vm.swappiness.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	VMSwappiness *int32 `json:"vmSwappiness,omitempty"`

	// VMVfsCachePressure specifies the percentage value that controls tendency of the kernel to reclaim the memory, which is used for caching of directory and inode objects.
	// Maps to vm.vfs_cache_pressure.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=500
	// +optional
	VMVfsCachePressure *int32 `json:"vmVfsCachePressure,omitempty"`
}

// LinuxOSConfig specifies the custom Linux OS settings and configurations.
type LinuxOSConfig struct {
	// SwapFileSizeMB specifies size in MB of a swap file will be created on the agent nodes from this node pool.
	// Max value of SwapFileSizeMB should be the size of temporary disk(/dev/sdb). Refer: https://learn.microsoft.com/en-us/azure/virtual-machines/managed-disks-overview#temporary-disk
	// +kubebuilder:validation:Minimum=1
	// +optional
	SwapFileSizeMB *int32 `json:"swapFileSizeMB,omitempty"`

	// Sysctl specifies the settings for Linux agent nodes.
	// +optional
	Sysctls *SysctlConfig `json:"sysctls,omitempty"`

	// TransparentHugePageDefrag specifies whether the kernel should make aggressive use of memory compaction to make more hugepages available.
	// Refer to https://www.kernel.org/doc/html/latest/admin-guide/mm/transhuge.html#admin-guide-transhuge for more details.
	// +kubebuilder:validation:Enum=always;defer;defer+madvise;madvise;never
	// +optional
	TransparentHugePageDefrag *TransparentHugePageOption `json:"transparentHugePageDefrag,omitempty"`

	// TransparentHugePageEnabled specifies various modes of Transparent Hugepages. Refer to https://www.kernel.org/doc/html/latest/admin-guide/mm/transhuge.html#admin-guide-transhuge for more details.
	// +kubebuilder:validation:Enum=always;madvise;never
	// +optional
	TransparentHugePageEnabled *TransparentHugePageOption `json:"transparentHugePageEnabled,omitempty"`
}

// AzureManagedMachinePoolSpec defines the desired state of AzureManagedMachinePool.
type AzureManagedMachinePoolSpec struct {

	// AdditionalTags is an optional set of tags to add to Azure resources managed by the
	// Azure provider, in addition to the ones added by default.
	// +optional
	AdditionalTags Tags `json:"additionalTags,omitempty"`

	// Name - name of the agent pool. If not specified, CAPZ uses the name of the CR as the agent pool name.
	// +optional
	Name *string `json:"name,omitempty"`

	// Mode - represents mode of an agent pool. Possible values include: System, User.
	// +kubebuilder:validation:Enum=System;User
	Mode string `json:"mode"`

	// SKU is the size of the VMs in the node pool.
	SKU string `json:"sku"`

	// OSDiskSizeGB is the disk size for every machine in this agent pool.
	// If you specify 0, it will apply the default osDisk size according to the vmSize specified.
	// +optional
	OSDiskSizeGB *int32 `json:"osDiskSizeGB,omitempty"`

	// AvailabilityZones - Availability zones for nodes. Must use VirtualMachineScaleSets AgentPoolType.
	// +optional
	AvailabilityZones []string `json:"availabilityZones,omitempty"`

	// Node labels - labels for all of the nodes present in node pool
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// Taints specifies the taints for nodes present in this agent pool.
	// +optional
	Taints Taints `json:"taints,omitempty"`

	// ProviderIDList is the unique identifier as specified by the cloud provider.
	// +optional
	ProviderIDList []string `json:"providerIDList,omitempty"`

	// Scaling specifies the autoscaling parameters for the node pool.
	// +optional
	Scaling *ManagedMachinePoolScaling `json:"scaling,omitempty"`

	// MaxPods specifies the kubelet --max-pods configuration for the node pool.
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`

	// OsDiskType specifies the OS disk type for each node in the pool. Allowed values are 'Ephemeral' and 'Managed'.
	// +kubebuilder:validation:Enum=Ephemeral;Managed
	// +kubebuilder:default=Managed
	// +optional
	OsDiskType *string `json:"osDiskType,omitempty"`

	// EnableUltraSSD enables the storage type UltraSSD_LRS for the agent pool.
	// +optional
	EnableUltraSSD *bool `json:"enableUltraSSD,omitempty"`

	// OSType specifies the virtual machine operating system. Default to Linux. Possible values include: 'Linux', 'Windows'
	// +kubebuilder:validation:Enum=Linux;Windows
	// +optional
	OSType *string `json:"osType,omitempty"`

	// EnableNodePublicIP controls whether or not nodes in the pool each have a public IP address.
	// +optional
	EnableNodePublicIP *bool `json:"enableNodePublicIP,omitempty"`

	// NodePublicIPPrefixID specifies the public IP prefix resource ID which VM nodes should use IPs from.
	// +optional
	NodePublicIPPrefixID *string `json:"nodePublicIPPrefixID,omitempty"`

	// ScaleSetPriority specifies the ScaleSetPriority value. Default to Regular. Possible values include: 'Regular', 'Spot'
	// +kubebuilder:validation:Enum=Regular;Spot
	// +optional
	ScaleSetPriority *string `json:"scaleSetPriority,omitempty"`

	// KubeletConfig specifies the kubelet configurations for nodes.
	// +optional
	KubeletConfig *KubeletConfig `json:"kubeletConfig,omitempty"`

	// KubeletDiskType specifies the kubelet disk type. Default to OS. Possible values include: 'OS', 'Temporary'.
	// Requires kubeletDisk preview feature to be set.
	// +kubebuilder:validation:Enum=OS;Temporary
	// +optional
	KubeletDiskType *KubeletDiskType `json:"kubeletDiskType,omitempty"`

	// LinuxOSConfig specifies the custom Linux OS settings and configurations.
	// +optional
	LinuxOSConfig *LinuxOSConfig `json:"linuxOSConfig,omitempty"`
}

// ManagedMachinePoolScaling specifies scaling options.
type ManagedMachinePoolScaling struct {
	MinSize *int32 `json:"minSize,omitempty"`
	MaxSize *int32 `json:"maxSize,omitempty"`
}

// TaintEffect is the effect for a Kubernetes taint.
type TaintEffect string

// Taint represents a Kubernetes taint.
type Taint struct {
	// Effect specifies the effect for the taint
	// +kubebuilder:validation:Enum=NoSchedule;NoExecute;PreferNoSchedule
	Effect TaintEffect `json:"effect"`
	// Key is the key of the taint
	Key string `json:"key"`
	// Value is the value of the taint
	Value string `json:"value"`
}

// Taints is an array of Taints.
type Taints []Taint

// AzureManagedMachinePoolStatus defines the observed state of AzureManagedMachinePool.
type AzureManagedMachinePoolStatus struct {
	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// Replicas is the most recently observed number of replicas.
	// +optional
	Replicas int32 `json:"replicas"`

	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	ErrorReason *capierrors.MachineStatusError `json:"errorReason,omitempty"`

	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	ErrorMessage *string `json:"errorMessage,omitempty"`

	// Conditions defines current service state of the AzureManagedControlPlane.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// LongRunningOperationStates saves the states for Azure long-running operations so they can be continued on the
	// next reconciliation loop.
	// +optional
	LongRunningOperationStates Futures `json:"longRunningOperationStates,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode"
// +kubebuilder:resource:path=azuremanagedmachinepools,scope=Namespaced,categories=cluster-api,shortName=ammp
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

// AzureManagedMachinePool is the Schema for the azuremanagedmachinepools API.
type AzureManagedMachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AzureManagedMachinePoolSpec   `json:"spec,omitempty"`
	Status AzureManagedMachinePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AzureManagedMachinePoolList contains a list of AzureManagedMachinePools.
type AzureManagedMachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AzureManagedMachinePool `json:"items"`
}

// GetConditions returns the list of conditions for an AzureManagedMachinePool API object.
func (m *AzureManagedMachinePool) GetConditions() clusterv1.Conditions {
	return m.Status.Conditions
}

// SetConditions will set the given conditions on an AzureManagedMachinePool object.
func (m *AzureManagedMachinePool) SetConditions(conditions clusterv1.Conditions) {
	m.Status.Conditions = conditions
}

// GetFutures returns the list of long running operation states for an AzureManagedMachinePool API object.
func (m *AzureManagedMachinePool) GetFutures() Futures {
	return m.Status.LongRunningOperationStates
}

// SetFutures will set the given long running operation states on an AzureManagedMachinePool object.
func (m *AzureManagedMachinePool) SetFutures(futures Futures) {
	m.Status.LongRunningOperationStates = futures
}

func init() {
	SchemeBuilder.Register(&AzureManagedMachinePool{}, &AzureManagedMachinePoolList{})
}
