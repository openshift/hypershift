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
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
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
	// +openshift:validation:FeatureGateAwareEnum:featureGate="",enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None
	// +openshift:validation:FeatureGateAwareEnum:featureGate=OpenStack,enum=AWS;Azure;IBMCloud;KubeVirt;Agent;PowerVS;None;OpenStack
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
	// +optional
	// +openshift:enable:FeatureGate=OpenStack
	OpenStack *OpenStackNodePoolPlatform `json:"openstack,omitempty"`
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

// NodePoolPlatformStatus contains specific platform statuses
type NodePoolPlatformStatus struct {
	// KubeVirt contains the KubeVirt platform statuses
	// +optional
	KubeVirt *KubeVirtNodePoolStatus `json:"kubeVirt,omitempty"`
}
