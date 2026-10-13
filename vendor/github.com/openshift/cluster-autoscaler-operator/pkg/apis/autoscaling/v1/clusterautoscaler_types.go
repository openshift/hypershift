package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&ClusterAutoscaler{}, &ClusterAutoscalerList{})
}

// ExpanderString contains the name of an expander to be used by the cluster autoscaler.
// +kubebuilder:validation:Enum=LeastWaste;Priority;Random
type ExpanderString string

// These constants define the valid values for an ExpanderString
const (
	LeastWasteExpander ExpanderString = "LeastWaste"
	PriorityExpander   ExpanderString = "Priority"
	RandomExpander     ExpanderString = "Random"
)

// ClusterAutoscalerSpec defines the desired state of ClusterAutoscaler
type ClusterAutoscalerSpec struct {
	// Constraints of autoscaling resources
	ResourceLimits *ResourceLimits `json:"resourceLimits,omitempty"`

	// Configuration of scale down operation
	ScaleDown *ScaleDownConfig `json:"scaleDown,omitempty"`

	// Gives pods graceful termination time before scaling down
	MaxPodGracePeriod *int32 `json:"maxPodGracePeriod,omitempty"`

	// Maximum time CA waits for node to be provisioned
	// +kubebuilder:validation:Pattern=^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$
	MaxNodeProvisionTime string `json:"maxNodeProvisionTime,omitempty"`

	// To allow users to schedule "best-effort" pods, which shouldn't trigger
	// Cluster Autoscaler actions, but only run when there are spare resources available,
	// More info: https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption
	PodPriorityThreshold *int32 `json:"podPriorityThreshold,omitempty"`

	// BalanceSimilarNodeGroups enables/disables the
	// `--balance-similar-node-groups` cluster-autoscaler feature.
	// This feature will automatically identify node groups with
	// the same instance type and the same set of labels and try
	// to keep the respective sizes of those node groups balanced.
	BalanceSimilarNodeGroups *bool `json:"balanceSimilarNodeGroups,omitempty"`

	// BalancingIgnoredLabels sets "--balancing-ignore-label <label name>" flag on cluster-autoscaler for each listed label.
	// This option specifies labels that cluster autoscaler should ignore when considering node group similarity.
	// For example, if you have nodes with "topology.ebs.csi.aws.com/zone" label, you can add name of this label here
	// to prevent cluster autoscaler from spliting nodes into different node groups based on its value.
	BalancingIgnoredLabels []string `json:"balancingIgnoredLabels,omitempty"`

	// Enables/Disables `--ignore-daemonsets-utilization` CA feature flag. Should CA ignore DaemonSet pods when calculating resource utilization for scaling down. false by default
	IgnoreDaemonsetsUtilization *bool `json:"ignoreDaemonsetsUtilization,omitempty"`

	// Enables/Disables `--skip-nodes-with-local-storage` CA feature flag. If true cluster autoscaler will never delete nodes with pods with local storage, e.g. EmptyDir or HostPath. true by default at autoscaler
	SkipNodesWithLocalStorage *bool `json:"skipNodesWithLocalStorage,omitempty"`

	// Sets the autoscaler log level.
	// Default value is 1, level 4 is recommended for DEBUGGING and level 6 will enable almost everything.
	//
	// This option has priority over log level set by the `CLUSTER_AUTOSCALER_VERBOSITY` environment variable.
	// +kubebuilder:validation:Minimum=0
	LogVerbosity *int32 `json:"logVerbosity,omitempty"`

	// Sets the type and order of expanders to be used during scale out operations.
	// This option specifies an ordered list, highest priority first, of expanders that
	// will be used by the cluster autoscaler to select node groups for expansion
	// when scaling out.
	// Expanders instruct the autoscaler on how to choose node groups when scaling out
	// the cluster. They can be specified in order so that the result from the first expander
	// is used as the input to the second, and so forth. For example, if set to `[LeastWaste, Random]`
	// the autoscaler will first evaluate node groups to determine which will have the least
	// resource waste, if multiple groups are selected the autoscaler will then randomly choose
	// between those groups to determine the group for scaling.
	// The following expanders are available:
	// * LeastWaste - selects the node group that will have the least idle CPU (if tied, unused memory) after scale-up.
	// * Priority - selects the node group that has the highest priority assigned by the user. For details, please see https://github.com/openshift/kubernetes-autoscaler/blob/master/cluster-autoscaler/expander/priority/readme.md
	// * Random - selects the node group randomly.
	// If not specified, the default value is `Random`, available options are: `LeastWaste`, `Priority`, `Random`.
	//
	// +listType=set
	// +kubebuilder:validation:MaxItems=3
	// +optional
	Expanders []ExpanderString `json:"expanders"`
}

// ClusterAutoscalerStatus defines the observed state of ClusterAutoscaler
type ClusterAutoscalerStatus struct {
	// TODO: Add status fields.
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterAutoscaler is the Schema for the clusterautoscalers API
// +k8s:openapi-gen=true
// +kubebuilder:resource:path=clusterautoscalers,shortName=ca,scope=Cluster
// +kubebuilder:subresource:status
// +genclient:nonNamespaced
type ClusterAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Desired state of ClusterAutoscaler resource
	Spec ClusterAutoscalerSpec `json:"spec,omitempty"`

	// Most recently observed status of ClusterAutoscaler resource
	Status ClusterAutoscalerStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterAutoscalerList contains a list of ClusterAutoscaler
type ClusterAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAutoscaler `json:"items"`
}

type ResourceLimits struct {
	// Maximum number of nodes in all node groups.
	// Cluster autoscaler will not grow the cluster beyond this number.
	// +kubebuilder:validation:Minimum=0
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"`

	// Minimum and maximum number of cores in cluster, in the format <min>:<max>.
	// Cluster autoscaler will not scale the cluster beyond these numbers.
	Cores *ResourceRange `json:"cores,omitempty"`

	// Minimum and maximum number of GiB of memory in cluster, in the format <min>:<max>.
	// Cluster autoscaler will not scale the cluster beyond these numbers.
	Memory *ResourceRange `json:"memory,omitempty"`

	// Minimum and maximum number of different GPUs in cluster, in the format <gpu_type>:<min>:<max>.
	// Cluster autoscaler will not scale the cluster beyond these numbers. Can be passed multiple times.
	GPUS []GPULimit `json:"gpus,omitempty"`
}

type GPULimit struct {
	// The type of GPU to associate with the minimum and maximum limits.
	// This value is used by the Cluster Autoscaler to identify Nodes that will have GPU capacity by searching
	// for it as a label value on the Node objects. For example, Nodes that carry the label key
	// `cluster-api/accelerator` with the label value being the same as the Type field will be counted towards
	// the resource limits by the Cluster Autoscaler.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// +kubebuilder:validation:Minimum=0
	Min int32 `json:"min"`
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
}

type ResourceRange struct {
	// +kubebuilder:validation:Minimum=0
	Min int32 `json:"min"`
	Max int32 `json:"max"`
}

type ScaleDownConfig struct {
	// Should CA scale down the cluster
	Enabled bool `json:"enabled"`

	// How long after scale up that scale down evaluation resumes
	// +kubebuilder:validation:Pattern=([0-9]*(\.[0-9]*)?[a-z]+)+
	DelayAfterAdd *string `json:"delayAfterAdd,omitempty"`

	// How long after node deletion that scale down evaluation resumes, defaults to scan-interval
	// +kubebuilder:validation:Pattern=([0-9]*(\.[0-9]*)?[a-z]+)+
	DelayAfterDelete *string `json:"delayAfterDelete,omitempty"`

	// How long after scale down failure that scale down evaluation resumes
	// +kubebuilder:validation:Pattern=([0-9]*(\.[0-9]*)?[a-z]+)+
	DelayAfterFailure *string `json:"delayAfterFailure,omitempty"`

	// How long a node should be unneeded before it is eligible for scale down
	// +kubebuilder:validation:Pattern=([0-9]*(\.[0-9]*)?[a-z]+)+
	UnneededTime *string `json:"unneededTime,omitempty"`

	// Node utilization level, defined as sum of requested resources divided by capacity, below which a node can be considered for scale down
	// +kubebuilder:validation:Pattern=(0.[0-9]+)
	UtilizationThreshold *string `json:"utilizationThreshold,omitempty"`
}
