package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	NodePoolValidHostedClusterConditionType      = "ValidHostedCluster"
	NodePoolValidReleaseImageConditionType       = "ValidReleaseImage"
	NodePoolValidAMIConditionType                = "ValidAMI"
	NodePoolValidPowerVSImageConditionType       = "ValidPowerVSImage"
	NodePoolValidKubeVirtImageConditionType      = "ValidKubeVirtImage"
	NodePoolValidMachineConfigConditionType      = "ValidMachineConfig"
	NodePoolValidKubevirtConfigConditionType     = "ValidKubevirtConfig"
	NodePoolUpdateManagementEnabledConditionType = "UpdateManagementEnabled"
	NodePoolAutoscalingEnabledConditionType      = "AutoscalingEnabled"
	NodePoolReadyConditionType                   = "Ready"
	NodePoolReconciliationActiveConditionType    = "ReconciliationActive"
	NodePoolAutorepairEnabledConditionType       = "AutorepairEnabled"
	NodePoolUpdatingVersionConditionType         = "UpdatingVersion"
	NodePoolUpdatingConfigConditionType          = "UpdatingConfig"
	NodePoolAsExpectedConditionReason            = "AsExpected"
	NodePoolValidationFailedConditionReason      = "ValidationFailed"
	NodePoolInplaceUpgradeFailedConditionReason  = "InplaceUpgradeFailed"
)

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	IgnitionEndpointMissingReason string = "IgnitionEndpointMissing"
	IgnitionCACertMissingReason   string = "IgnitionCACertMissing"
)

const (
	// IgnitionServerTokenExpirationTimestampAnnotation holds the time that a ignition token expires and should be
	// removed from the cluster.
	IgnitionServerTokenExpirationTimestampAnnotation = "hypershift.openshift.io/ignition-token-expiration-timestamp"
)

func init() {
	SchemeBuilder.Register(&NodePool{})
	SchemeBuilder.Register(&NodePoolList{})
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
type NodePoolSpec struct {
	// ClusterName is the name of the HostedCluster this NodePool belongs to.
	//
	// TODO(dan): Should this be a LocalObjectReference?
	//
	// +immutable
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

	// Deprecated: Use Replicas instead. NodeCount will be dropped in the next
	// api release.
	//
	// +optional
	NodeCount *int32 `json:"nodeCount,omitempty"`

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
	// Each ConfigMap must have a single key named "config" whose value is the
	// JSON or YAML of a serialized MachineConfig.
	// +kubebuilder:validation:Optional
	Config []corev1.LocalObjectReference `json:"config,omitempty"`

	// NodeDrainTimeout is the total amount of time that the controller will spend on draining a node.
	// The default value is 0, meaning that the node can be drained without any time limitations.
	// NOTE: NodeDrainTimeout is different from `kubectl drain --timeout`
	// TODO (alberto): Today changing this field will trigger a recreate rolling update, which kind of defeats
	// the purpose of the change. In future we plan to propagate this field in-place.
	// https://github.com/kubernetes-sigs/cluster-api/issues/5880
	// +optional
	NodeDrainTimeout *metav1.Duration `json:"nodeDrainTimeout,omitempty"`

	// PausedUntil is a field that can be used to pause reconciliation on a resource.
	// Either a date can be provided in RFC3339 format or a boolean. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
	// +optional
	PausedUntil *string `json:"pausedUntil,omitempty"`
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

	// Conditions represents the latest available observations of the node pool's
	// current state.
	Conditions []NodePoolCondition `json:"conditions"`
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
type InPlaceUpgrade struct{}

// NodePoolManagement specifies behavior for managing nodes in a NodePool, such
// as upgrade strategies and auto-repair behaviors.
type NodePoolManagement struct {
	// UpgradeType specifies the type of strategy for handling upgrades.
	//
	// +kubebuilder:validation:Enum=Replace;InPlace
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
	AutoRepair bool `json:"autoRepair"`
}

// NodePoolAutoScaling specifies auto-scaling behavior for a NodePool.
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
}

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
	// reasonable default. The current default is Shared.
	//
	// +kubebuilder:default=shared
	// +kubebuilder:validation:Enum=dedicated;shared;capped
	// +optional
	ProcessorType string `json:"processorType,omitempty"`

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
	StorageType string `json:"storageType,omitempty"`

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
	ImageDeletePolicy string `json:"imageDeletePolicy,omitempty"`
}

// KubevirtCompute contains values associated with the virtual compute hardware requested for the VM.
type KubevirtCompute struct {
	// Memory represents how much guest memory the VM should have
	//
	// +optional
	// +kubebuilder:default="4Gi"
	Memory *resource.Quantity `json:"memory"`

	// Cores represents how many cores the guest VM should have
	//
	// +optional
	// +kubebuilder:default=2
	Cores *uint32 `json:"cores"`
}

// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany;ReadOnly;ReadWriteOncePod
type PersistentVolumeAccessMode corev1.PersistentVolumeAccessMode

// KubevirtPersistentVolume contains the values involved with provisioning persistent storage for a KubeVirt VM.
type KubevirtPersistentVolume struct {
	// Size is the size of the persistent storage volume
	//
	// +optional
	// +kubebuilder:default="16Gi"
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
}

// KubevirtRootVolume represents the volume that the rhcos disk will be stored and run from.
type KubevirtRootVolume struct {
	// Image represents what rhcos image to use for the node pool
	//
	// +optional
	Image *KubevirtDiskImage `json:"diskImage,omitempty"`

	// KubevirtVolume represents of type of storage to run the image on
	KubevirtVolume `json:",inline"`
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

// KubevirtNodePoolPlatform specifies the configuration of a NodePool when operating
// on KubeVirt platform.
type KubevirtNodePoolPlatform struct {
	// RootVolume represents values associated with the VM volume that will host rhcos
	RootVolume *KubevirtRootVolume `json:"rootVolume"`

	// Compute contains values representing the virtual hardware requested for the VM
	//
	// +optional
	// +kubebuilder:default={memory: "4Gi", cores: 2}
	Compute *KubevirtCompute `json:"compute"`
}

// AWSNodePoolPlatform specifies the configuration of a NodePool when operating
// on AWS.
type AWSNodePoolPlatform struct {
	// InstanceType is an ec2 instance type for node instances (e.g. m5.large).
	InstanceType string `json:"instanceType"`

	// InstanceProfile is the AWS EC2 instance profile, which is a container for an IAM role that the EC2 instance uses.
	InstanceProfile string `json:"instanceProfile,omitempty"`

	// Subnet is the subnet to use for node instances.
	//
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

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
}

// AWSResourceReference is a reference to a specific AWS resource by ID, ARN, or filters.
// Only one of ID, ARN or Filters may be specified. Specifying more than one will result in
// a validation error.
type AWSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// ARN of resource
	// +optional
	ARN *string `json:"arn,omitempty"`

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
}

// AgentNodePoolPlatform specifies the configuration of a NodePool when operating
// on the Agent platform.
type AgentNodePoolPlatform struct {
	// AgentLabelSelector contains labels that must be set on an Agent in order to
	// be selected for a Machine.
	// +optional
	AgentLabelSelector *metav1.LabelSelector `json:"agentLabelSelector,omitempty"`
}

type AzureNodePoolPlatform struct {
	VMSize string `json:"vmsize"`
	// ImageID is the id of the image to boot from. If unset, the default image at the location below will be used:
	// subscription/$subscriptionID/resourceGroups/$resourceGroupName/providers/Microsoft.Compute/images/rhcos.x86_64.vhd
	// +optional
	ImageID string `json:"imageID,omitempty"`
	// +kubebuilder:default:=120
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
	// AvailabilityZone of the nodepool. Must not be specified for clusters
	// in a location that does not support AvailabilityZone.
	// +optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`
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
