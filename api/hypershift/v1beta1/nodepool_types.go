package v1beta1

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/api/ibmcapi"
)

const (
	// TODO - for the next API bump; the case on these needs to be changed.
	// See https://github.com/openshift/hypershift/pull/4538#discussion_r1765165827 for more details.
	ArchitectureAMD64   = "amd64"
	ArchitectureS390X   = "s390x"
	ArchitecturePPC64LE = "ppc64le"
	ArchitectureARM64   = "arm64"
	ArchitectureMulti   = "multi"

	// NodePoolLabel is used to label Nodes.
	NodePoolLabel = "hypershift.openshift.io/nodePool"

	// IgnitionServerTokenExpirationTimestampAnnotation holds the time that a ignition token expires and should be
	// removed from the cluster.
	IgnitionServerTokenExpirationTimestampAnnotation = "hypershift.openshift.io/ignition-token-expiration-timestamp"
)

var (
	// ArchAliases contains the RHCOS release metadata aliases for the different architectures supported as API input.
	ArchAliases = map[string]string{
		ArchitectureAMD64: "x86_64",
		ArchitectureARM64: "aarch64",
	}
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&NodePool{},
			&NodePoolList{},
		)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	})
}

// +genclient

// NodePool is a scalable set of worker nodes attached to a HostedCluster.
// NodePool machine architectures are uniform within a given pool, and are
// independent of the control plane’s underlying machine architecture.
//
// +kubebuilder:resource:path=nodepools,shortName=np;nps,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterName",description="Cluster"
// +kubebuilder:printcolumn:name="Desired Nodes",type="integer",JSONPath=".spec.replicas",description="Desired Nodes"
// +kubebuilder:printcolumn:name="Current Nodes",type="integer",JSONPath=".status.replicas",description="Available Nodes"
// +kubebuilder:printcolumn:name="Autoscaling",type="string",JSONPath=".status.conditions[?(@.type==\"AutoscalingEnabled\")].status",description="Autoscaling Enabled"
// +kubebuilder:printcolumn:name="Autorepair",type="string",JSONPath=".status.conditions[?(@.type==\"AutorepairEnabled\")].status",description="Node Autorepair Enabled"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="Current version"
// +kubebuilder:printcolumn:name="UpdatingVersion",type="string",JSONPath=".status.conditions[?(@.type==\"UpdatingVersion\")].status",description="UpdatingVersion in progress"
// +kubebuilder:printcolumn:name="UpdatingConfig",type="string",JSONPath=".status.conditions[?(@.type==\"UpdatingConfig\")].status",description="UpdatingConfig in progress"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description="Message"
type NodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the NodePool.
	Spec NodePoolSpec `json:"spec,omitempty"`

	// Status is the latest observed status of the NodePool.
	Status NodePoolStatus `json:"status,omitempty"`
}

// NodePoolSpec is the desired behavior of a NodePool.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.arch) || has(self.arch)", message="Arch is required once set"
// +kubebuilder:validation:XValidation:rule="self.arch != 'arm64' || has(self.platform.aws) || has(self.platform.azure)", message="Setting Arch to arm64 is only supported for AWS and Azure"
// +kubebuilder:validation:XValidation:rule="!has(self.replicas) || !has(self.autoScaling)", message="Both replicas or autoScaling should not be set"
type NodePoolSpec struct {
	// ClusterName is the name of the HostedCluster this NodePool belongs to.
	//
	// TODO(dan): Should this be a LocalObjectReference?
	//
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="ClusterName is immutable"
	ClusterName string `json:"clusterName"`

	// Release specifies the OCP release used for the NodePool. This informs the
	// ignition configuration for machines, as well as other platform specific
	// machine properties (e.g. an AMI on the AWS platform).
	Release Release `json:"release"`

	// Platform specifies the underlying infrastructure provider for the NodePool
	// and is used to configure platform specific behavior.
	//
	// +immutable
	Platform NodePoolPlatform `json:"platform"`

	// Replicas is the desired number of nodes the pool should maintain. If
	// unset, the default value is 0.
	//
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Management specifies behavior for managing nodes in the pool, such as
	// upgrade strategies and auto-repair behaviors.
	Management NodePoolManagement `json:"management"`

	// Autoscaling specifies auto-scaling behavior for the NodePool.
	//
	// +optional
	AutoScaling *NodePoolAutoScaling `json:"autoScaling,omitempty"`

	// Config is a list of references to ConfigMaps containing serialized
	// MachineConfig resources to be injected into the ignition configurations of
	// nodes in the NodePool. The MachineConfig API schema is defined here:
	//
	// https://github.com/openshift/machine-config-operator/blob/18963e4f8fe66e8c513ca4b131620760a414997f/pkg/apis/machineconfiguration.openshift.io/v1/types.go#L185
	//
	// Each ConfigMap must have a single key named "config" whose value is the YML
	// with one or more serialized machineconfiguration.openshift.io resources:
	// KubeletConfig
	// ContainerRuntimeConfig
	// MachineConfig
	// ClusterImagePolicy
	// ImageContentSourcePolicy
	// or
	// ImageDigestMirrorSet
	//
	// +kubebuilder:validation:Optional
	Config []corev1.LocalObjectReference `json:"config,omitempty"`

	// NodeDrainTimeout is the maximum amount of time that the controller will spend on draining a node.
	// The default value is 0, meaning that the node can be drained without any time limitations.
	// NOTE: NodeDrainTimeout is different from `kubectl drain --timeout`
	// TODO (alberto): Today changing this field will trigger a recreate rolling update, which kind of defeats
	// the purpose of the change. In future we plan to propagate this field in-place.
	// https://github.com/kubernetes-sigs/cluster-api/issues/5880 / https://github.com/kubernetes-sigs/cluster-api/pull/10589
	// +optional
	NodeDrainTimeout *metav1.Duration `json:"nodeDrainTimeout,omitempty"`

	// NodeVolumeDetachTimeout is the maximum amount of time that the controller will spend on detaching volumes from a node.
	// The default value is 0, meaning that the volumes will be detached from the node without any time limitations.
	// After the timeout, the detachment of volumes that haven't been detached yet is skipped.
	// TODO (cbusse): Same comment as Alberto's for `NodeDrainTimeout`:
	// Today changing this field will trigger a recreate rolling update, which kind of defeats
	// the purpose of the change. In future we plan to propagate this field in-place.
	// https://github.com/kubernetes-sigs/cluster-api/issues/5880 / https://github.com/kubernetes-sigs/cluster-api/pull/10589
	// +optional
	NodeVolumeDetachTimeout *metav1.Duration `json:"nodeVolumeDetachTimeout,omitempty"`

	// NodeLabels propagates a list of labels to Nodes, only once on creation.
	// Valid values are those in https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// Taints if specified, propagates a list of taints to Nodes, only once on creation.
	// +optional
	Taints []Taint `json:"taints,omitempty"`

	// PausedUntil is a field that can be used to pause reconciliation on a resource.
	// Either a date can be provided in RFC3339 format or a boolean. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
	// +optional
	PausedUntil *string `json:"pausedUntil,omitempty"`

	// TuningConfig is a list of references to ConfigMaps containing serialized
	// Tuned or PerformanceProfile resources to define the tuning configuration to be applied to
	// nodes in the NodePool. The Tuned API is defined here:
	//
	// https://github.com/openshift/cluster-node-tuning-operator/blob/2c76314fb3cc8f12aef4a0dcd67ddc3677d5b54f/pkg/apis/tuned/v1/tuned_types.go
	//
	// The PerformanceProfile API is defined here:
	// https://github.com/openshift/cluster-node-tuning-operator/tree/b41042d42d4ba5bb2e99960248cf1d6ae4935018/pkg/apis/performanceprofile/v2
	//
	// Each ConfigMap must have a single key named "tuning" whose value is the
	// JSON or YAML of a serialized Tuned or PerformanceProfile.
	// +kubebuilder:validation:Optional
	TuningConfig []corev1.LocalObjectReference `json:"tuningConfig,omitempty"`

	// Arch is the preferred processor architecture for the NodePool (currently only supported on AWS)
	// NOTE: This is set as optional to prevent validation from failing due to a limitation on client side validation with open API machinery:
	//	https://github.com/kubernetes/kubernetes/issues/108768#issuecomment-1253912215
	// TODO Add s390x to enum validation once the architecture is supported
	//
	// +kubebuilder:default:=amd64
	// +kubebuilder:validation:Enum=arm64;amd64;ppc64le
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Arch is immutable"
	// +optional
	Arch string `json:"arch,omitempty"`
}

// NodePoolStatus is the latest observed status of a NodePool.
type NodePoolStatus struct {
	// Replicas is the latest observed number of nodes in the pool.
	//
	// +optional
	Replicas int32 `json:"replicas"`

	// Version is the semantic version of the latest applied release specified by
	// the NodePool.
	//
	// +kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`

	// Platform hols the specific statuses
	Platform *NodePoolPlatformStatus `json:"platform,omitempty"`

	// Conditions represents the latest available observations of the node pool's
	// current state.
	// +optional
	Conditions []NodePoolCondition `json:"conditions,omitempty"`
}

// NodePoolList contains a list of NodePools.
//
// +kubebuilder:object:root=true
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodePool `json:"items"`
}

// UpgradeType is a type of high-level upgrade behavior nodes in a NodePool.
type UpgradeType string

const (
	// UpgradeTypeReplace is a strategy which replaces nodes using surge node
	// capacity.
	UpgradeTypeReplace = UpgradeType("Replace")

	// UpgradeTypeInPlace is a strategy which replaces nodes in-place with no
	// additional node capacity requirements.
	UpgradeTypeInPlace = UpgradeType("InPlace")
)

func (p *UpgradeType) String() string {
	return string(*p)
}

func (p *UpgradeType) Set(s string) error {
	switch strings.ToLower(s) {
	case "replace":
		*p = UpgradeTypeReplace
	case "inplace":
		*p = UpgradeTypeInPlace
	default:
		return fmt.Errorf("unknown upgrade type used '%s'", s)
	}
	return nil
}

func (p *UpgradeType) Type() string {
	return "UpgradeType"
}

// UpgradeStrategy is a specific strategy for upgrading nodes in a NodePool.
type UpgradeStrategy string

const (
	// UpgradeStrategyRollingUpdate means use a rolling update for nodes.
	UpgradeStrategyRollingUpdate = UpgradeStrategy("RollingUpdate")

	// UpgradeStrategyOnDelete replaces old nodes when the deletion of the
	// associated node instances are completed.
	UpgradeStrategyOnDelete = UpgradeStrategy("OnDelete")
)

// ReplaceUpgrade specifies upgrade behavior that replaces existing nodes
// according to a given strategy.
type ReplaceUpgrade struct {
	// Strategy is the node replacement strategy for nodes in the pool.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=RollingUpdate;OnDelete
	Strategy UpgradeStrategy `json:"strategy"`

	// RollingUpdate specifies a rolling update strategy which upgrades nodes by
	// creating new nodes and deleting the old ones.
	//
	// +kubebuilder:validation:Optional
	RollingUpdate *RollingUpdate `json:"rollingUpdate,omitempty"`
}

// RollingUpdate specifies a rolling update strategy which upgrades nodes by
// creating new nodes and deleting the old ones.
type RollingUpdate struct {
	// MaxUnavailable is the maximum number of nodes that can be unavailable
	// during the update.
	//
	// Value can be an absolute number (ex: 5) or a percentage of desired nodes
	// (ex: 10%).
	//
	// Absolute number is calculated from percentage by rounding down.
	//
	// This can not be 0 if MaxSurge is 0.
	//
	// Defaults to 0.
	//
	// Example: when this is set to 30%, old nodes can be deleted down to 70% of
	// desired nodes immediately when the rolling update starts. Once new nodes
	// are ready, more old nodes be deleted, followed by provisioning new nodes,
	// ensuring that the total number of nodes available at all times during the
	// update is at least 70% of desired nodes.
	//
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// MaxSurge is the maximum number of nodes that can be provisioned above the
	// desired number of nodes.
	//
	// Value can be an absolute number (ex: 5) or a percentage of desired nodes
	// (ex: 10%).
	//
	// Absolute number is calculated from percentage by rounding up.
	//
	// This can not be 0 if MaxUnavailable is 0.
	//
	// Defaults to 1.
	//
	// Example: when this is set to 30%, new nodes can be provisioned immediately
	// when the rolling update starts, such that the total number of old and new
	// nodes do not exceed 130% of desired nodes. Once old nodes have been
	// deleted, new nodes can be provisioned, ensuring that total number of nodes
	// running at any time during the update is at most 130% of desired nodes.
	//
	// +optional
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`
}

// InPlaceUpgrade specifies an upgrade strategy which upgrades nodes in-place
// without any new nodes being created or any old nodes being deleted.
type InPlaceUpgrade struct {
	// MaxUnavailable is the maximum number of nodes that can be unavailable
	// during the update.
	//
	// Value can be an absolute number (ex: 5) or a percentage of desired nodes
	// (ex: 10%).
	//
	// Absolute number is calculated from percentage by rounding down.
	//
	// Defaults to 1.
	//
	// Example: when this is set to 30%, a max of 30% of the nodes can be made
	// unschedulable/unavailable immediately when the update starts. Once a set
	// of nodes is updated, more nodes can be made unschedulable for update,
	// ensuring that the total number of nodes schedulable at all times during
	// the update is at least 70% of desired nodes.
	//
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// NodePoolManagement specifies behavior for managing nodes in a NodePool, such
// as upgrade strategies and auto-repair behaviors.
type NodePoolManagement struct {
	// UpgradeType specifies the type of strategy for handling upgrades.
	//
	// +kubebuilder:validation:Enum=Replace;InPlace
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="UpgradeType is immutable"
	UpgradeType UpgradeType `json:"upgradeType"`

	// Replace is the configuration for rolling upgrades.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={strategy: "RollingUpdate", rollingUpdate: {maxSurge: 1, maxUnavailable: 0 }}
	Replace *ReplaceUpgrade `json:"replace,omitempty"`

	// InPlace is the configuration for in-place upgrades.
	//
	// +kubebuilder:validation:Optional
	InPlace *InPlaceUpgrade `json:"inPlace,omitempty"`

	// AutoRepair specifies whether health checks should be enabled for machines
	// in the NodePool. The default is false.
	//
	// +optional
	// +kubebuilder:default=false
	AutoRepair bool `json:"autoRepair"`
}

// NodePoolAutoScaling specifies auto-scaling behavior for a NodePool.
// +kubebuilder:validation:XValidation:rule="self.max >= self.min", message="max must be equal or greater than min"
type NodePoolAutoScaling struct {
	// Min is the minimum number of nodes to maintain in the pool. Must be >= 1.
	//
	// +kubebuilder:validation:Minimum=1
	Min int32 `json:"min"`

	// Max is the maximum number of nodes allowed in the pool. Must be >= 1.
	//
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
}

// NodePoolPlatform specifies the underlying infrastructure provider for the
// NodePool and is used to configure platform specific behavior.
type NodePoolPlatform struct {
	// Type specifies the platform name.
	//
	// +unionDiscriminator
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Type is immutable"
	// +immutable
	Type PlatformType `json:"type"`

	// AWS specifies the configuration used when operating on AWS.
	//
	// +optional
	AWS *AWSNodePoolPlatform `json:"aws,omitempty"`

	// IBMCloud defines IBMCloud specific settings for components
	IBMCloud *IBMCloudPlatformSpec `json:"ibmcloud,omitempty"`

	// Kubevirt specifies the configuration used when operating on KubeVirt platform.
	//
	// +optional
	Kubevirt *KubevirtNodePoolPlatform `json:"kubevirt,omitempty"`

	// Agent specifies the configuration used when using Agent platform.
	//
	// +optional
	Agent *AgentNodePoolPlatform `json:"agent,omitempty"`

	Azure *AzureNodePoolPlatform `json:"azure,omitempty"`

	// PowerVS specifies the configuration used when using IBMCloud PowerVS platform.
	//
	// +optional
	PowerVS *PowerVSNodePoolPlatform `json:"powervs,omitempty"`

	// OpenStack specifies the configuration used when using OpenStack platform.
	//
	// +optional
	OpenStack *OpenStackNodePoolPlatform `json:"openstack,omitempty"`
}

// PowerVSNodePoolProcType defines processor type to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolProcType string

func (p *PowerVSNodePoolProcType) String() string {
	return string(*p)
}

func (p *PowerVSNodePoolProcType) Set(s string) error {
	switch s {
	case string(PowerVSNodePoolSharedProcType), string(PowerVSNodePoolCappedProcType), string(PowerVSNodePoolDedicatedProcType):
		*p = PowerVSNodePoolProcType(s)
		return nil
	default:
		return fmt.Errorf("unknown processor type used %s", s)
	}
}

func (p *PowerVSNodePoolProcType) Type() string {
	return "PowerVSNodePoolProcType"
}

const (
	// PowerVSNodePoolDedicatedProcType defines dedicated processor type
	PowerVSNodePoolDedicatedProcType = PowerVSNodePoolProcType("dedicated")

	// PowerVSNodePoolSharedProcType defines shared processor type
	PowerVSNodePoolSharedProcType = PowerVSNodePoolProcType("shared")

	// PowerVSNodePoolCappedProcType defines capped processor type
	PowerVSNodePoolCappedProcType = PowerVSNodePoolProcType("capped")
)

func (p *PowerVSNodePoolProcType) CastToCAPIPowerVSProcessorType() ibmcapi.PowerVSProcessorType {
	switch *p {
	case PowerVSNodePoolDedicatedProcType:
		return ibmcapi.PowerVSProcessorTypeDedicated
	case PowerVSNodePoolCappedProcType:
		return ibmcapi.PowerVSProcessorTypeCapped
	default:
		return ibmcapi.PowerVSProcessorTypeShared
	}
}

// PowerVSNodePoolStorageType defines storage type to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolStorageType string

// PowerVSNodePoolImageDeletePolicy defines image delete policy to be used for PowerVSNodePoolPlatform
type PowerVSNodePoolImageDeletePolicy string

// PowerVSNodePoolPlatform specifies the configuration of a NodePool when operating
// on IBMCloud PowerVS platform.
type PowerVSNodePoolPlatform struct {
	// SystemType is the System type used to host the instance.
	// systemType determines the number of cores and memory that is available.
	// Few of the supported SystemTypes are s922,e880,e980.
	// e880 systemType available only in Dallas Datacenters.
	// e980 systemType available in Datacenters except Dallas and Washington.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The current default is s922 which is generally available.
	//
	// +optional
	// +kubebuilder:default=s922
	SystemType string `json:"systemType,omitempty"`

	// ProcessorType is the VM instance processor type.
	// It must be set to one of the following values: Dedicated, Capped or Shared.
	//
	// Dedicated: resources are allocated for a specific client, The hypervisor makes a 1:1 binding of a partition’s processor to a physical processor core.
	// Shared: Shared among other clients.
	// Capped: Shared, but resources do not expand beyond those that are requested, the amount of CPU time is Capped to the value specified for the entitlement.
	//
	// if the processorType is selected as Dedicated, then Processors value cannot be fractional.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The current default is shared.
	//
	// +kubebuilder:default=shared
	// +kubebuilder:validation:Enum=dedicated;shared;capped
	// +optional
	ProcessorType PowerVSNodePoolProcType `json:"processorType,omitempty"`

	// Processors is the number of virtual processors in a virtual machine.
	// when the processorType is selected as Dedicated the processors value cannot be fractional.
	// maximum value for the Processors depends on the selected SystemType.
	// when SystemType is set to e880 or e980 maximum Processors value is 143.
	// when SystemType is set to s922 maximum Processors value is 15.
	// minimum value for Processors depends on the selected ProcessorType.
	// when ProcessorType is set as Shared or Capped, The minimum processors is 0.5.
	// when ProcessorType is set as Dedicated, The minimum processors is 1.
	// When omitted, this means that the user has no opinion and the platform is left to choose a
	// reasonable default. The default is set based on the selected ProcessorType.
	// when ProcessorType selected as Dedicated, the default is set to 1.
	// when ProcessorType selected as Shared or Capped, the default is set to 0.5.
	//
	// +optional
	// +kubebuilder:default="0.5"
	Processors intstr.IntOrString `json:"processors,omitempty"`

	// MemoryGiB is the size of a virtual machine's memory, in GiB.
	// maximum value for the MemoryGiB depends on the selected SystemType.
	// when SystemType is set to e880 maximum MemoryGiB value is 7463 GiB.
	// when SystemType is set to e980 maximum MemoryGiB value is 15307 GiB.
	// when SystemType is set to s922 maximum MemoryGiB value is 942 GiB.
	// The minimum memory is 32 GiB.
	//
	// When omitted, this means the user has no opinion and the platform is left to choose a reasonable
	// default. The current default is 32.
	//
	// +optional
	// +kubebuilder:default=32
	MemoryGiB int32 `json:"memoryGiB,omitempty"`

	// Image used for deploying the nodes. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	Image *PowerVSResourceReference `json:"image,omitempty"`

	// StorageType for the image and nodes, this will be ignored if Image is specified.
	// The storage tiers in PowerVS are based on I/O operations per second (IOPS).
	// It means that the performance of your storage volumes is limited to the maximum number of IOPS based on volume size and storage tier.
	// Although, the exact numbers might change over time, the Tier 3 storage is currently set to 3 IOPS/GB, and the Tier 1 storage is currently set to 10 IOPS/GB.
	//
	// The default is tier1
	//
	// +kubebuilder:default=tier1
	// +kubebuilder:validation:Enum=tier1;tier3
	// +optional
	StorageType PowerVSNodePoolStorageType `json:"storageType,omitempty"`

	// ImageDeletePolicy is policy for the image deletion.
	//
	// delete: delete the image from the infrastructure.
	// retain: delete the image from the openshift but retain in the infrastructure.
	//
	// The default is delete
	//
	// +kubebuilder:default=delete
	// +kubebuilder:validation:Enum=delete;retain
	// +optional
	ImageDeletePolicy PowerVSNodePoolImageDeletePolicy `json:"imageDeletePolicy,omitempty"`
}

type QoSClass string

const (
	QoSClassBurstable  QoSClass = "Burstable"
	QoSClassGuaranteed QoSClass = "Guaranteed"
)

// KubevirtCompute contains values associated with the virtual compute hardware requested for the VM.
type KubevirtCompute struct {
	// Memory represents how much guest memory the VM should have
	//
	// +optional
	// +kubebuilder:default="8Gi"
	Memory *resource.Quantity `json:"memory"`

	// Cores represents how many cores the guest VM should have
	//
	// +optional
	// +kubebuilder:default=2
	Cores *uint32 `json:"cores"`

	// QosClass If set to "Guaranteed", requests the scheduler to place the VirtualMachineInstance on a node with
	// limit memory and CPU, equal to be the requested values, to set the VMI as a Guaranteed QoS Class;
	// See here for more details:
	// https://kubevirt.io/user-guide/operations/node_overcommit/#requesting-the-right-qos-class-for-virtualmachineinstances
	//
	// +optional
	// +kubebuilder:validation:Enum=Burstable;Guaranteed
	// +kubebuilder:default=Burstable
	QosClass *QoSClass `json:"qosClass,omitempty"`
}

// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany;ReadOnly;ReadWriteOncePod
type PersistentVolumeAccessMode corev1.PersistentVolumeAccessMode

// KubevirtPersistentVolume contains the values involved with provisioning persistent storage for a KubeVirt VM.
type KubevirtPersistentVolume struct {
	// Size is the size of the persistent storage volume
	//
	// +optional
	// +kubebuilder:default="32Gi"
	Size *resource.Quantity `json:"size"`
	// StorageClass is the storageClass used for the underlying PVC that hosts the volume
	//
	// +optional
	StorageClass *string `json:"storageClass,omitempty"`
	// AccessModes is an array that contains the desired Access Modes the root volume should have.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes
	//
	// +optional
	AccessModes []PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	// VolumeMode defines what type of volume is required by the claim.
	// Value of Filesystem is implied when not included in claim spec.
	// +optional
	// +kubebuilder:validation:Enum=Filesystem;Block
	VolumeMode *corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`
}

// KubevirtCachingStrategyType is the type of the boot image caching mechanism for the KubeVirt provider
type KubevirtCachingStrategyType string

const (
	// KubevirtCachingStrategyNone means that hypershift will not cache the boot image
	KubevirtCachingStrategyNone KubevirtCachingStrategyType = "None"

	// KubevirtCachingStrategyPVC means that hypershift will cache the boot image into a PVC; only relevant when using
	// a QCOW boot image, and is ignored when using a container image
	KubevirtCachingStrategyPVC KubevirtCachingStrategyType = "PVC"
)

// KubevirtCachingStrategy defines the boot image caching strategy
type KubevirtCachingStrategy struct {
	// Type is the type of the caching strategy
	// +kubebuilder:default=None
	// +kubebuilder:validation:Enum=None;PVC
	Type KubevirtCachingStrategyType `json:"type"`
}

// KubevirtRootVolume represents the volume that the rhcos disk will be stored and run from.
type KubevirtRootVolume struct {
	// Image represents what rhcos image to use for the node pool
	//
	// +optional
	Image *KubevirtDiskImage `json:"diskImage,omitempty"`

	// KubevirtVolume represents of type of storage to run the image on
	KubevirtVolume `json:",inline"`

	// CacheStrategy defines the boot image caching strategy. Default - no caching
	// +optional
	CacheStrategy *KubevirtCachingStrategy `json:"cacheStrategy,omitempty"`
}

// KubevirtVolumeType is a specific supported KubeVirt volumes
//
// +kubebuilder:validation:Enum=Persistent
type KubevirtVolumeType string

const (
	// KubevirtVolumeTypePersistent represents persistent volume for kubevirt VMs
	KubevirtVolumeTypePersistent KubevirtVolumeType = "Persistent"
)

// KubevirtVolume represents what kind of storage to use for a KubeVirt VM volume
type KubevirtVolume struct {
	// Type represents the type of storage to associate with the kubevirt VMs.
	//
	// +optional
	// +unionDiscriminator
	// +kubebuilder:default=Persistent
	Type KubevirtVolumeType `json:"type"`

	// Persistent volume type means the VM's storage is backed by a PVC
	// VMs that use persistent volumes can survive disruption events like restart and eviction
	// This is the default type used when no storage type is defined.
	//
	// +optional
	Persistent *KubevirtPersistentVolume `json:"persistent,omitempty"`
}

// KubevirtDiskImage contains values representing where the rhcos image is located
type KubevirtDiskImage struct {
	// ContainerDiskImage is a string representing the container image that holds the root disk
	//
	// +optional
	ContainerDiskImage *string `json:"containerDiskImage,omitempty"`
}

type MultiQueueSetting string

const (
	MultiQueueEnable  MultiQueueSetting = "Enable"
	MultiQueueDisable MultiQueueSetting = "Disable"
)

// KubevirtNodePoolPlatform specifies the configuration of a NodePool when operating
// on KubeVirt platform.
type KubevirtNodePoolPlatform struct {
	// RootVolume represents values associated with the VM volume that will host rhcos
	// +kubebuilder:default={persistent: {size: "32Gi"}, type: "Persistent"}
	RootVolume *KubevirtRootVolume `json:"rootVolume"`

	// Compute contains values representing the virtual hardware requested for the VM
	//
	// +optional
	// +kubebuilder:default={memory: "8Gi", cores: 2}
	Compute *KubevirtCompute `json:"compute"`

	// NetworkInterfaceMultiQueue If set to "Enable", virtual network interfaces configured with a virtio bus will also
	// enable the vhost multiqueue feature for network devices. The number of queues created depends on additional
	// factors of the VirtualMachineInstance, like the number of guest CPUs.
	//
	// +optional
	// +kubebuilder:validation:Enum=Enable;Disable
	// +kubebuilder:default=Enable
	NetworkInterfaceMultiQueue *MultiQueueSetting `json:"networkInterfaceMultiqueue,omitempty"`

	// AdditionalNetworks specify the extra networks attached to the nodes
	//
	// +optional
	AdditionalNetworks []KubevirtNetwork `json:"additionalNetworks,omitempty"`

	// AttachDefaultNetwork specify if the default pod network should be attached to the nodes
	// this can only be set to false if AdditionalNetworks are configured
	//
	// +optional
	// +kubebuilder:default=true
	AttachDefaultNetwork *bool `json:"attachDefaultNetwork,omitempty"`

	// NodeSelector is a selector which must be true for the kubevirt VirtualMachine to fit on a node.
	// Selector which must match a node's labels for the VM to be scheduled on that node. More info:
	// https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// KubevirtHostDevices specifies the host devices (e.g. GPU devices) to be passed
	// from the management cluster, to the nodepool nodes
	KubevirtHostDevices []KubevirtHostDevice `json:"hostDevices,omitempty"`
}

// KubevirtNetwork specifies the configuration for a virtual machine
// network interface
type KubevirtNetwork struct {
	// Name specify the network attached to the nodes
	// it is a value with the format "[namespace]/[name]" to reference the
	// multus network attachment definition
	Name string `json:"name"`
}

type KubevirtHostDevice struct {
	// DeviceName is the name of the host device that is desired to be utilized in the HostedCluster's NodePool
	// The device can be any supported PCI device, including GPU, either as a passthrough or a vGPU slice.
	DeviceName string `json:"deviceName"`

	// Count is the number of instances the specified host device will be attached to each of the
	// NodePool's nodes. Default is 1.
	//
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Count int `json:"count,omitempty"`
}

// AWSNodePoolPlatform specifies the configuration of a NodePool when operating
// on AWS.
type AWSNodePoolPlatform struct {
	// InstanceType is an ec2 instance type for node instances (e.g. m5.large).
	InstanceType string `json:"instanceType"`

	// InstanceProfile is the AWS EC2 instance profile, which is a container for an IAM role that the EC2 instance uses.
	InstanceProfile string `json:"instanceProfile,omitempty"`

	// +kubebuilder:validation:XValidation:rule="has(self.id) && self.id.startsWith('subnet-') ? !has(self.filters) : size(self.filters) > 0", message="subnet is invalid, a valid subnet id or filters must be set, but not both"
	// +kubebuilder:validation:Required
	//
	// Subnet is the subnet to use for node instances.
	Subnet AWSResourceReference `json:"subnet"`

	// AMI is the image id to use for node instances. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	AMI string `json:"ami,omitempty"`

	// SecurityGroups is an optional set of security groups to associate with node
	// instances.
	//
	// +optional
	SecurityGroups []AWSResourceReference `json:"securityGroups,omitempty"`

	// RootVolume specifies configuration for the root volume of node instances.
	//
	// +optional
	RootVolume *Volume `json:"rootVolume,omitempty"`

	// ResourceTags is an optional list of additional tags to apply to AWS node
	// instances.
	//
	// These will be merged with HostedCluster scoped tags, and HostedCluster tags
	// take precedence in case of conflicts.
	//
	// See https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for
	// information on tagging AWS resources. AWS supports a maximum of 50 tags per
	// resource. OpenShift reserves 25 tags for its use, leaving 25 tags available
	// for the user.
	//
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// placement specifies the placement options for the EC2 instances.
	//
	// +optional
	Placement *PlacementOptions `json:"placement,omitempty"`
}

// PlacementOptions specifies the placement options for the EC2 instances.
type PlacementOptions struct {
	// Tenancy indicates if instance should run on shared or single-tenant hardware.
	//
	// Possible values:
	// default: NodePool instances run on shared hardware.
	// dedicated: Each NodePool instance runs on single-tenant hardware.
	// host: NodePool instances run on user's pre-allocated dedicated hosts.
	//
	// +optional
	// +kubebuilder:validation:Enum:=default;dedicated;host
	Tenancy string `json:"tenancy,omitempty"`
}

// AWSResourceReference is a reference to a specific AWS resource by ID or filters.
// Only one of ID or Filters may be specified. Specifying more than one will result in
// a validation error.
type AWSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// Filters is a set of key/value pairs used to identify a resource
	// They are applied according to the rules defined by the AWS API:
	// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Filtering.html
	// +optional
	Filters []Filter `json:"filters,omitempty"`
}

// Filter is a filter used to identify an AWS resource
type Filter struct {
	// Name of the filter. Filter names are case-sensitive.
	Name string `json:"name"`

	// Values includes one or more filter values. Filter values are case-sensitive.
	Values []string `json:"values"`
}

// Volume specifies the configuration options for node instance storage devices.
type Volume struct {
	// Size specifies size (in Gi) of the storage device.
	//
	// Must be greater than the image snapshot size or 8 (whichever is greater).
	//
	// +kubebuilder:validation:Minimum=8
	Size int64 `json:"size"`

	// Type is the type of the volume.
	Type string `json:"type"`

	// IOPS is the number of IOPS requested for the disk. This is only valid
	// for type io1.
	//
	// +optional
	IOPS int64 `json:"iops,omitempty"`

	// Encrypted is whether the volume should be encrypted or not.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Encrypted is immutable"
	Encrypted *bool `json:"encrypted,omitempty"`

	// EncryptionKey is the KMS key to use to encrypt the volume. Can be either a KMS key ID or ARN.
	// If Encrypted is set and this is omitted, the default AWS key will be used.
	// The key must already exist and be accessible by the controller.
	// +optional
	EncryptionKey string `json:"encryptionKey,omitempty"`
}

// AgentNodePoolPlatform specifies the configuration of a NodePool when operating
// on the Agent platform.
type AgentNodePoolPlatform struct {
	// AgentLabelSelector contains labels that must be set on an Agent in order to
	// be selected for a Machine.
	// +optional
	AgentLabelSelector *metav1.LabelSelector `json:"agentLabelSelector,omitempty"`
}

type OpenStackNodePoolPlatform struct {
	// Flavor is the OpenStack flavor to use for the node instances.
	//
	// +kubebuilder:validation:Required
	// +required
	Flavor string `json:"flavor"`

	// ImageName is the OpenStack Glance image name to use for node instances. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	ImageName string `json:"imageName,omitempty"`
}

type AzureVMImageType string

const (
	ImageID          AzureVMImageType = "ImageID"
	AzureMarketplace AzureVMImageType = "AzureMarketplace"
)

// AzureVMImage represents the different types of image data that can be provided for an Azure VM.
// +union
type AzureVMImage struct {
	// Type is the type of image data that will be provided to the Azure VM. This can be either "ImageID" or
	// "AzureMarketplace".
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=ImageID;AzureMarketplace
	// +unionDiscriminator
	Type AzureVMImageType `json:"azureImageType"`

	// ImageID is the Azure resource ID of a VHD image to use to boot the Azure VMs from.
	//
	// +optional
	// +unionMember
	ImageID *string `json:"imageID,omitempty"`

	// AzureMarketplace contains the Azure Marketplace image info to use to boot the Azure VMs from.
	//
	// +optional
	// +unionMember
	AzureMarketplace *MarketplaceImage `json:"azureMarketplace,omitempty"`
}

type AzureNodePoolPlatform struct {
	// VMSize is the Azure VM instance type to use for the nodes being created in the nodepool.
	//
	// +kubebuilder:validation:Required
	VMSize string `json:"vmsize"`

	// ImageID is the id of the image to boot from. If unset, the default image at the location below will be used and
	// is expected to exist: subscription/<subscriptionID>/resourceGroups/<resourceGroupName>/providers/Microsoft.Compute/images/rhcos.x86_64.vhd.
	// The <subscriptionID> and the <resourceGroupName> are expected to be the same resource group documented in the
	// Hosted Cluster specification respectively, HostedCluster.Spec.Platform.Azure.SubscriptionID and
	// HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// +kubebuilder:validation:Required
	Image AzureVMImage `json:"image"`

	// DiskSizeGB is the size in GB to assign to the OS disk
	// CAPZ default is 30GB, https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/b3708019a67ff19407b87d63c402af94ca4246f6/api/v1beta1/types.go#L599
	//
	// +kubebuilder:default:=30
	// +kubebuilder:validation:Minimum=16
	// +optional
	DiskSizeGB int32 `json:"diskSizeGB,omitempty"`

	// DiskStorageAccountType is the disk storage account type to use. Valid values are:
	// * Standard_LRS: HDD
	// * StandardSSD_LRS: Standard SSD
	// * Premium_LRS: Premium SDD
	// * UltraSSD_LRS: Ultra SDD
	//
	// Defaults to Premium_LRS. For more details, visit the Azure documentation:
	// https://docs.microsoft.com/en-us/azure/virtual-machines/disks-types#disk-type-comparison
	//
	// +kubebuilder:default:=Premium_LRS
	// +kubebuilder:validation:Enum=Standard_LRS;StandardSSD_LRS;Premium_LRS;UltraSSD_LRS
	// +optional
	DiskStorageAccountType string `json:"diskStorageAccountType,omitempty"`

	// AvailabilityZone is the failure domain identifier where the VM should be attached to. This must not be specified
	// for clusters in a location that does not support AvailabilityZone.
	//
	// +optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// encryptionAtHost enables encryption at host on virtual machines. According to Microsoft documentation, this
	// means data stored on the VM host is encrypted at rest and flows encrypted to the Storage service. See
	// https://learn.microsoft.com/en-us/azure/virtual-machines/disks-enable-host-based-encryption-portal?tabs=azure-powershell
	// for more information.
	//
	// +kubebuilder:default:=Enabled
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// +optional
	EncryptionAtHost string `json:"encryptionAtHost,omitempty"`

	// DiskEncryptionSetID is the ID of the DiskEncryptionSet resource to use to encrypt the OS disks for the VMs. This
	// needs to exist in the same subscription id listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.SubscriptionID.
	// DiskEncryptionSetID should also exist in a resource group under the same subscription id and the same location
	// listed in the Hosted Cluster, HostedCluster.Spec.Platform.Azure.Location.
	//
	// +optional
	DiskEncryptionSetID string `json:"diskEncryptionSetID,omitempty"`

	// EnableEphemeralOSDisk is a flag when set to true, will enable ephemeral OS disk.
	//
	// +optional
	EnableEphemeralOSDisk bool `json:"enableEphemeralOSDisk,omitempty"`

	// SubnetID is the subnet ID of an existing subnet where the nodes in the nodepool will be created. This can be a
	// different subnet than the one listed in the HostedCluster, HostedCluster.Spec.Platform.Azure.SubnetID, but must
	// exist in the same HostedCluster.Spec.Platform.Azure.VnetID and must exist under the same subscription ID,
	// HostedCluster.Spec.Platform.Azure.SubscriptionID.
	//
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="SubnetID is immutable"
	// +kubebuilder:validation:Required
	SubnetID string `json:"subnetID"`

	// Diagnostics specifies the diagnostics settings for a virtual machine.
	// If not specified, then Boot diagnostics will be disabled.
	// +optional
	Diagnostics *Diagnostics `json:"diagnostics,omitempty"`

	// MachineIdentityID is a user-assigned identity assigned to the VMs used to authenticate with Azure services. This
	// field is expected to exist under the same resource group as HostedCluster.Spec.Platform.Azure.ResourceGroupName. This
	// user assigned identity is expected to have the Contributor role assigned to it and scoped to the resource group
	// under HostedCluster.Spec.Platform.Azure.ResourceGroupName.
	//
	// If this field is not supplied, the Service Principal credentials will be written to a file on the disk of each VM
	// in order to be accessible by the cloud provider; the aforementioned credentials provided are the same ones as
	// HostedCluster.Spec.Platform.Azure.Credentials. However, this is less secure than using a managed identity.
	//
	// +optional
	MachineIdentityID string `json:"machineIdentityID,omitempty"`
}

// MarketplaceImage specifies the information needed to create an Azure VM from an Azure Marketplace image. This struct
// replicates the same fields found in CAPZ - https://github.com/kubernetes-sigs/cluster-api-provider-azure/blob/main/api/v1beta1/types.go.
type MarketplaceImage struct {
	// Publisher is the name of the organization that created the image
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-_]{2,49}$`
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=50
	Publisher string `json:"publisher"`

	// Offer specifies the name of a group of related images created by the publisher.
	//
	// +kubebuilder:validation:MinLength=1
	Offer string `json:"offer"`

	// SKU specifies an instance of an offer, such as a major release of a distribution.
	// For example, 18.04-LTS, 2019-Datacenter
	//
	// +kubebuilder:validation:Pattern=`^[a-z0-9-_]+$`
	// +kubebuilder:validation:MinLength=1
	SKU string `json:"sku"`

	// Version specifies the version of an image sku. The allowed formats are Major.Minor.Build or 'latest'. Major,
	// Minor, and Build are decimal numbers. Specify 'latest' to use the latest version of an image available at
	// deployment time. Even if you use 'latest', the VM image will not automatically update after deploy time even if a
	// new version becomes available.
	//
	// +kubebuilder:validation:Pattern=`^[0-9]+\.[0-9]+\.[0-9]+$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	Version string `json:"version"`
}

// We define our own condition type since metav1.Condition has validation
// for Reason that might be broken by what we bubble up from CAPI.
// NodePoolCondition defines an observation of NodePool resource operational state.
type NodePoolCondition struct {
	// Type of condition in CamelCase or in foo.example.com/CamelCase.
	// Many .condition.type values are consistent across resources like Available, but because arbitrary conditions
	// can be useful (see .node.status.conditions), the ability to deconflict is important.
	Type string `json:"type"`

	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`

	// Severity provides an explicit classification of Reason code, so the users or machines can immediately
	// understand the current situation and act accordingly.
	// The Severity field MUST be set only when Status=False.
	// +optional
	Severity string `json:"severity,omitempty"`

	// Last time the condition transitioned from one status to another.
	// This should be when the underlying condition changed. If that is not known, then using the time when
	// the API field changed is acceptable.
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`

	// The reason for the condition's last transition in CamelCase.
	// The specific API may choose whether or not this field is considered a guaranteed API.
	// This field may not be empty.
	// +optional
	Reason string `json:"reason,omitempty"`

	// A human readable message indicating details about the transition.
	// This field may be empty.
	// +optional
	Message string `json:"message,omitempty"`

	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// NodePoolPlatformStatus contains specific platform statuses
type NodePoolPlatformStatus struct {
	// KubeVirt contains the KubeVirt platform statuses
	// +optional
	KubeVirt *KubeVirtNodePoolStatus `json:"kubeVirt,omitempty"`
}

// KubeVirtNodePoolStatus contains the KubeVirt platform statuses
type KubeVirtNodePoolStatus struct {
	// CacheName holds the name of the cache DataVolume, if exists
	// +optional
	CacheName string `json:"cacheName,omitempty"`

	// Credentials shows the client credentials used when creating KubeVirt virtual machines.
	// This filed is only exists when the KubeVirt virtual machines are being placed
	// on a cluster separate from the one hosting the Hosted Control Plane components.
	//
	// The default behavior when Credentials is not defined is for the KubeVirt VMs to be placed on
	// the same cluster and namespace as the Hosted Control Plane.
	// +optional
	Credentials *KubevirtPlatformCredentials `json:"credentials,omitempty"`
}

// Taint is as v1 Core but without TimeAdded.
// https://github.com/kubernetes/kubernetes/blob/ed8cad1e80d096257921908a52ac69cf1f41a098/staging/src/k8s.io/api/core/v1/types.go#L3037-L3053
type Taint struct {
	// Required. The taint key to be applied to a node.
	Key string `json:"key"`
	// The taint value corresponding to the taint key.
	// +optional
	// +kubebuilder:validation:Pattern:=`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`
	Value string `json:"value,omitempty"`
	// Required. The effect of the taint on pods
	// that do not tolerate the taint.
	// Valid effects are NoSchedule, PreferNoSchedule and NoExecute.
	Effect corev1.TaintEffect `json:"effect"`
}

// AzureDiagnosticsStorageAccountType specifies the type of storage account for storing Azure VM diagnostics data.
// +kubebuilder:validation:Enum=Managed;UserManaged;Disabled
type AzureDiagnosticsStorageAccountType string

func (a *AzureDiagnosticsStorageAccountType) String() string {
	return string(*a)
}

func (a *AzureDiagnosticsStorageAccountType) Set(s string) error {
	switch s {
	case string(AzureDiagnosticsStorageAccountTypeDisabled), string(AzureDiagnosticsStorageAccountTypeManaged), string(AzureDiagnosticsStorageAccountTypeUserManaged):
		*a = AzureDiagnosticsStorageAccountType(s)
		return nil
	default:
		return fmt.Errorf("unknown Azure diagnostics storage account type: %s", s)
	}
}

func (a *AzureDiagnosticsStorageAccountType) Type() string {
	return "AzureDiagnosticsStorageAccountType"
}

const (
	AzureDiagnosticsStorageAccountTypeDisabled    = AzureDiagnosticsStorageAccountType("Disabled")
	AzureDiagnosticsStorageAccountTypeManaged     = AzureDiagnosticsStorageAccountType("Managed")
	AzureDiagnosticsStorageAccountTypeUserManaged = AzureDiagnosticsStorageAccountType("UserManaged")
)

// Diagnostics specifies the diagnostics settings for a virtual machine.
// +kubebuilder:validation:XValidation:rule="self.storageAccountType == 'UserManaged' ? has(self.storageAccountURI) : true", message="storageAccountURI is required when storageAccountType is UserManaged"
type Diagnostics struct {
	// StorageAccountType determines if the storage account for storing the diagnostics data
	// should be disabled (Disabled), provisioned by Azure (Managed) or by the user (UserManaged).
	// +kubebuilder:default:=Disabled
	StorageAccountType AzureDiagnosticsStorageAccountType `json:"storageAccountType,omitempty"`
	// StorageAccountURI is the URI of the user-managed storage account.
	// The URI typically will be `https://<mystorageaccountname>.blob.core.windows.net/`
	// but may differ if you are using Azure DNS zone endpoints.
	// You can find the correct endpoint by looking for the Blob Primary Endpoint in the
	// endpoints tab in the Azure console or with the CLI by issuing
	// `az storage account list --query='[].{name: name, "resource group": resourceGroup, "blob endpoint": primaryEndpoints.blob}'`.
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	StorageAccountURI string `json:"storageAccountURI,omitempty"`
}
