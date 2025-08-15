package v1beta1

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
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
// independent of the control plane's underlying machine architecture.
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
	metav1.TypeMeta `json:",inline"`

	// metadata is the metadata for the NodePool.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the desired behavior of the NodePool.
	// +optional
	Spec NodePoolSpec `json:"spec,omitempty"`

	// status is the latest observed status of the NodePool.
	// +optional
	Status NodePoolStatus `json:"status,omitempty"`
}

// NodePoolSpec is the desired behavior of a NodePool.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.arch) || has(self.arch)", message="Arch is required once set"
// +kubebuilder:validation:XValidation:rule="self.arch != 'arm64' || has(self.platform.aws) || has(self.platform.azure) || has(self.platform.agent) || self.platform.type == 'None'", message="Setting Arch to arm64 is only supported for AWS, Azure, Agent and None"
// +kubebuilder:validation:XValidation:rule="!has(self.replicas) || !has(self.autoScaling)", message="Both replicas or autoScaling should not be set"
type NodePoolSpec struct {
	// clusterName is the name of the HostedCluster this NodePool belongs to.
	// If a HostedCluster with this name doesn't exist, the controller will no-op until it exists.
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="ClusterName is immutable"
	// +required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="clusterName must consist of lowercase alphanumeric characters or '-', start and end with an alphanumeric character, and be between 1 and 253 characters"
	ClusterName string `json:"clusterName"`

	// release specifies the OCP release used for the NodePool. This informs the
	// ignition configuration for machines which includes the kubelet version, as well as other platform specific
	// machine properties (e.g. an AMI on the AWS platform).
	// It's not supported to use a release in a NodePool which minor version skew against the Control Plane release is bigger than N-2. Although there's no enforcement that prevents this from happening.
	// Attempting to use a release with a bigger skew might result in unpredictable behaviour.
	// Attempting to use a release higher than the HosterCluster one will result in the NodePool being degraded and the ValidReleaseImage condition being false.
	// Attempting to use a release lower than the current NodePool y-stream will result in the NodePool being degraded and the ValidReleaseImage condition being false.
	// Changing this field will trigger a NodePool rollout.
	// +rollout
	// +required
	Release Release `json:"release"`

	// platform specifies the underlying infrastructure provider for the NodePool
	// and is used to configure platform specific behavior.
	// +required
	Platform NodePoolPlatform `json:"platform"`

	// replicas is the desired number of nodes the pool should maintain. If unset, the controller default value is 0.
	// replicas is mutually exclusive with autoscaling. If autoscaling is configured, replicas must be omitted and autoscaling will control the NodePool size internally.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// management specifies behavior for managing nodes in the pool, such as
	// upgrade strategies and auto-repair behaviors.
	// +required
	Management NodePoolManagement `json:"management"`

	// autoScaling specifies auto-scaling behavior for the NodePool.
	// autoScaling is mutually exclusive with replicas. If replicas is set, this field must be omitted.
	//
	// +optional
	AutoScaling *NodePoolAutoScaling `json:"autoScaling,omitempty"`

	// config is a list of references to ConfigMaps containing serialized
	// MachineConfig resources to be injected into the ignition configurations of
	// nodes in the NodePool. The MachineConfig API schema is defined here:
	//
	// https://github.com/openshift/machine-config-operator/blob/18963e4f8fe66e8c513ca4b131620760a414997f/pkg/apis/machineconfiguration.openshift.io/v1/types.go#L185
	//
	// Each ConfigMap must have a single key named "config" whose value is the YML
	// with one or more serialized machineconfiguration.openshift.io resources:
	//
	// * KubeletConfig
	// * ContainerRuntimeConfig
	// * MachineConfig
	// * ClusterImagePolicy
	// * ImageContentSourcePolicy
	// * ImageDigestMirrorSet
	//
	// This is validated in the backend and signaled back via validMachineConfig condition.
	// Changing this field will trigger a NodePool rollout.
	// +rollout
	// +optional
	// +kubebuilder:validation:MaxItems=10
	Config []corev1.LocalObjectReference `json:"config,omitempty"`

	// nodeDrainTimeout is the maximum amount of time that the controller will spend on retrying to drain a node until it succeeds.
	// The default value is 0, meaning that the node can retry drain without any time limitations.
	// Changing this field propagate inplace into existing Nodes.
	// +optional
	NodeDrainTimeout *metav1.Duration `json:"nodeDrainTimeout,omitempty"`

	// nodeVolumeDetachTimeout is the maximum amount of time that the controller will spend on detaching volumes from a node.
	// The default value is 0, meaning that the volumes will be detached from the node without any time limitations.
	// After the timeout, any remaining attached volumes will be ignored and the removal of the machine will continue.
	// Changing this field propagate inplace into existing Nodes.
	// +optional
	NodeVolumeDetachTimeout *metav1.Duration `json:"nodeVolumeDetachTimeout,omitempty"`

	// nodeLabels propagates a list of labels to Nodes, only once on creation.
	// Valid values are those in https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// taints if specified, propagates a list of taints to Nodes, only once on creation.
	// These taints are additive to the ones applied by other controllers
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Taints []Taint `json:"taints,omitempty"`

	// pausedUntil is a field that can be used to pause reconciliation on the NodePool controller. Resulting in any change to the NodePool being ignored.
	// Either a date can be provided in RFC3339 format or a boolean as in 'true', 'false', 'True', 'False'. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
	// +kubebuilder:validation:MaxLength=35
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule=`self.matches('^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}.*$') || self in ['true', 'false', 'True', 'False']`,message="PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'"
	// +optional
	PausedUntil *string `json:"pausedUntil,omitempty"`

	// tuningConfig is a list of references to ConfigMaps containing serialized
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
	// Changing this field will trigger a NodePool rollout.
	// +optional
	// +kubebuilder:validation:MaxItems=10
	TuningConfig []corev1.LocalObjectReference `json:"tuningConfig,omitempty"`

	// arch is the preferred processor architecture for the NodePool. Different platforms might have different supported architectures.
	// TODO: This is set as optional to prevent validation from failing due to a limitation on client side validation with open API machinery:
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
	// replicas is the latest observed number of nodes in the pool.
	//
	// +optional
	Replicas int32 `json:"replicas"`

	// version is the semantic version of the latest applied release specified by
	// the NodePool.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version,omitempty"`

	// platform holds the specific statuses
	// +optional
	Platform *NodePoolPlatformStatus `json:"platform,omitempty"`

	// conditions represents the latest available observations of the node pool's
	// current state.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Conditions []NodePoolCondition `json:"conditions,omitempty"`
}

// NodePoolList contains a list of NodePools.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the metadata for the NodePoolList.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is a list of NodePools.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Items []NodePool `json:"items"`
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
// +kubebuilder:validation:XValidation:rule="!has(self.rollingUpdate) || self.strategy == 'RollingUpdate'", message="The 'rollingUpdate' field can only be set when 'strategy' is 'RollingUpdate'"
type ReplaceUpgrade struct {
	// strategy is the node replacement strategy for nodes in the pool.
	// In can be either "RollingUpdate" or "OnDelete". RollingUpdate will rollout Nodes honoring maxSurge and maxUnavailable.
	// OnDelete provide more granular control and will replace nodes as the old ones are manually deleted.
	// +optional
	// +kubebuilder:validation:Enum=RollingUpdate;OnDelete
	Strategy UpgradeStrategy `json:"strategy,omitempty"`

	// rollingUpdate specifies a rolling update strategy which upgrades nodes by
	// creating new nodes and deleting the old ones.
	//
	// +optional
	RollingUpdate *RollingUpdate `json:"rollingUpdate,omitempty"`
}

// RollingUpdate specifies a rolling update strategy which upgrades nodes by
// creating new nodes and deleting the old ones.
type RollingUpdate struct {
	// maxUnavailable is the maximum number of nodes that can be unavailable
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

	// maxSurge is the maximum number of nodes that can be provisioned above the
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
	// maxUnavailable is the maximum number of nodes that can be unavailable
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
// +kubebuilder:validation:XValidation:rule="!has(self.inPlace) || self.upgradeType == 'InPlace'", message="The 'inPlace' field can only be set when 'upgradeType' is 'InPlace'"
type NodePoolManagement struct {
	// upgradeType specifies the type of strategy for handling upgrades.
	// This can be either "Replace" or "InPlace".
	// "Replace" will update Nodes by recreating the underlying instances.
	// "InPlace" will update Nodes by applying changes to the existing instances. This might or might not result in a reboot.
	//
	// +kubebuilder:validation:Enum=Replace;InPlace
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="UpgradeType is immutable"
	// +required
	UpgradeType UpgradeType `json:"upgradeType"`

	// replace is the configuration for rolling upgrades.
	// It defaults to a RollingUpdate strategy with maxSurge of 1 and maxUnavailable of 0.
	//
	// +kubebuilder:default={strategy: "RollingUpdate", rollingUpdate: {maxSurge: 1, maxUnavailable: 0 }}
	// +optional
	Replace *ReplaceUpgrade `json:"replace,omitempty"`

	// inPlace is the configuration for in-place upgrades.
	//
	// +optional
	InPlace *InPlaceUpgrade `json:"inPlace,omitempty"`

	// autoRepair specifies whether health checks should be enabled for machines in the NodePool. The default is false.
	// Enabling this feature will cause the controller to automatically delete unhealthy machines.
	// The unhealthy criteria is reserved for the controller implementation and subject to change.
	// But generally it's determined by checking the Node ready condition is true and a timeout that might vary depending on the platform provider.
	// AutoRepair will no-op when more than 2 Nodes are unhealthy at the same time. Giving time for the cluster to stabilize or to the user to manually intervene.
	// +optional
	// +kubebuilder:default=false
	AutoRepair bool `json:"autoRepair"` //nolint:kubeapilinter
}

// NodePoolAutoScaling specifies auto-scaling behavior for a NodePool.
// +kubebuilder:validation:XValidation:rule="self.max >= self.min", message="max must be equal or greater than min"
type NodePoolAutoScaling struct {
	// min is the minimum number of nodes to maintain in the pool. Must be >= 1 and <= .Max.
	//
	// +kubebuilder:validation:Minimum=1
	// +required
	Min int32 `json:"min"`

	// max is the maximum number of nodes allowed in the pool. Must be >= 1 and >= Min.
	//
	// +kubebuilder:validation:Minimum=1
	// +required
	Max int32 `json:"max"`
}

// NodePoolPlatform specifies the underlying infrastructure provider for the
// NodePool and is used to configure platform specific behavior.
type NodePoolPlatform struct {
	// type specifies the platform name.
	//
	// +unionDiscriminator
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="Type is immutable"
	// +immutable
	// +openshift:validation:FeatureGateAwareEnum:featureGate="",enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None
	// +openshift:validation:FeatureGateAwareEnum:featureGate=OpenStack,enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None;OpenStack
	// +required
	Type PlatformType `json:"type"`

	// aws specifies the configuration used when operating on AWS.
	//
	// +optional
	AWS *AWSNodePoolPlatform `json:"aws,omitempty"`

	// ibmcloud defines IBMCloud specific settings for components
	//
	// +optional
	IBMCloud *IBMCloudPlatformSpec `json:"ibmcloud,omitempty"`

	// kubevirt specifies the configuration used when operating on KubeVirt platform.
	//
	// +optional
	Kubevirt *KubevirtNodePoolPlatform `json:"kubevirt,omitempty"`

	// agent specifies the configuration used when using Agent platform.
	//
	// +optional
	Agent *AgentNodePoolPlatform `json:"agent,omitempty"`

	// azure specifies the configuration used when using Azure platform.
	//
	// +optional
	Azure *AzureNodePoolPlatform `json:"azure,omitempty"`

	// powervs specifies the configuration used when using IBMCloud PowerVS platform.
	//
	// +optional
	PowerVS *PowerVSNodePoolPlatform `json:"powervs,omitempty"`

	// openstack specifies the configuration used when using OpenStack platform.
	// +optional
	// +openshift:enable:FeatureGate=OpenStack
	OpenStack *OpenStackNodePoolPlatform `json:"openstack,omitempty"`
}

// We define our own condition type since metav1.Condition has validation
// for Reason that might be broken by what we bubble up from CAPI.
// NodePoolCondition defines an observation of NodePool resource operational state.
type NodePoolCondition struct {
	// type of condition in CamelCase or in foo.example.com/CamelCase.
	// Many .condition.type values are consistent across resources like Available, but because arbitrary conditions
	// can be useful (see .node.status.conditions), the ability to deconflict is important.
	// +kubebuilder:validation:MaxLength=316
	// +required
	Type string `json:"type"`

	// status of the condition, one of True, False, Unknown.
	// +required
	Status corev1.ConditionStatus `json:"status"`

	// severity provides an explicit classification of Reason code, so the users or machines can immediately
	// understand the current situation and act accordingly.
	// The Severity field MUST be set only when Status=False.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Severity string `json:"severity,omitempty"`

	// lastTransitionTime is the last time the condition transitioned from one status to another.
	// This should be when the underlying condition changed. If that is not known, then using the time when
	// the API field changed is acceptable.
	// +required
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`

	// reason for the condition's last transition in CamelCase.
	// The specific API may choose whether or not this field is considered a guaranteed API.
	// This field may not be empty.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Reason string `json:"reason,omitempty"`

	// message is a human readable message indicating details about the transition.
	// This field may be empty.
	// +kubebuilder:validation:MaxLength=32768
	// +optional
	Message string `json:"message,omitempty"`

	// observedGeneration represents the .metadata.generation that the condition was set based upon.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// taint is as v1 Core but without TimeAdded.
// https://github.com/kubernetes/kubernetes/blob/ed8cad1e80d096257921908a52ac69cf1f41a098/staging/src/k8s.io/api/core/v1/types.go#L3037-L3053
// Validation replicates the same validation as the upstream https://github.com/kubernetes/kubernetes/blob/9a2a7537f035969a68e432b4cc276dbce8ce1735/pkg/util/taints/taints.go#L273.
// See also https://kubernetes.io/docs/concepts/overview/working-with-objects/names/.
type Taint struct {
	// key is the taint key to be applied to a node.
	// +required
	// +kubebuilder:validation:XValidation:rule=`self.matches('^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*\\/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$')`,message="key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// value is the taint value corresponding to the taint key.
	// +optional
	// +kubebuilder:validation:XValidation:rule=`self.matches('^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$')`,message="Value must start and end with alphanumeric characters and can only contain '-', '_', '.' in the middle"
	// +kubebuilder:validation:MaxLength=253
	Value string `json:"value,omitempty"`

	// effect is the effect of the taint on pods
	// +required
	// that do not tolerate the taint.
	// Valid effects are NoSchedule, PreferNoSchedule and NoExecute.
	// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
	Effect corev1.TaintEffect `json:"effect"`
}

// NodePoolPlatformStatus struct contains platform-specific status information.
type NodePoolPlatformStatus struct {
	// kubeVirt contains the KubeVirt platform statuses
	// +optional
	KubeVirt *KubeVirtNodePoolStatus `json:"kubeVirt,omitempty"`
}
