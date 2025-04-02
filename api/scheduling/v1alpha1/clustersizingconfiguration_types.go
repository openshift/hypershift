package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:resource:path=clustersizingconfigurations,shortName=csc;cscs,scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +genclient:nonNamespaced
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'", message="exactly one configuration may exist and must be named 'cluster'"

// ClusterSizingConfiguration defines the desired state of ClusterSizingConfiguration.
// Configuration options here allow management cluster administrators to define sizing classes for hosted clusters and
// how the system should adapt hosted cluster functionality based on size.
type ClusterSizingConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSizingConfigurationSpec   `json:"spec,omitempty"`
	Status ClusterSizingConfigurationStatus `json:"status,omitempty"`
}

// ClusterSizingConfigurationSpec defines the desired state of ClusterSizingConfiguration
type ClusterSizingConfigurationSpec struct {
	// +listType=map
	// +listMapKey=name
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.exists_one(i, i.criteria.from == 0)", message="exactly one size class must have a lower limit of zero"
	// +kubebuilder:validation:XValidation:rule="self.exists_one(i, !has(i.criteria.to))", message="exactly one size class must have no upper limit"

	// Sizes holds the different t-shirt size classes into which guest clusters will be sorted.
	// Each size class applies to guest clusters using node count criteria; it is required that
	// the entire interval between [0,+inf) be covered by the set of sizes provided here.
	Sizes []SizeConfiguration `json:"sizes,omitempty"`

	// +kubebuilder:validation:Optional

	// Concurrency defines the bounds of allowed behavior for clusters transitioning between sizes.
	// Transitions will require that request-serving pods be re-scheduled between nodes, so each
	// transition incurs a small user-facing cost as well as a cost to the management cluster. Use
	// the concurrency configuration options to manage how many transitions can be occurring.
	// These limits do not apply to new clusters entering the fleet.
	// If unset, a sensible default will be provided.
	Concurrency ConcurrencyConfiguration `json:"concurrency,omitempty"`

	// +kubebuilder:validation:Optional

	// TransitionDelay configures how quickly the system reacts to clusters transitioning between size classes.
	// It may be advantageous, for instance, to have a near-instant scale-down for clusters that begin to
	// use fewer resources, but allow for some lag on scale-up to ensure that the use is sustained before
	// incurring the larger cost for scale-up.
	TransitionDelay TransitionDelayConfiguration `json:"transitionDelay,omitempty"`

	// +kubebuilder:validation:Optional

	// NonRequestServingNodesBufferPerZone is the number of extra nodes to allocate for non request serving
	// workloads per zone.
	NonRequestServingNodesBufferPerZone *resource.Quantity `json:"nonRequestServingNodesBufferPerZone,omitempty"`
}

// SizeConfiguration holds options for clusters of a given size.
type SizeConfiguration struct {
	// +kubebuilder:validation:Required

	// Name is the t-shirt size name.
	Name string `json:"name"`

	// +kubebuilder:validation:Required

	// Criteria defines the node count range for clusters to fall into this t-shirt size class.
	Criteria NodeCountCriteria `json:"criteria"`

	// +kubebuilder:validation:Optional

	// Effects define the effects on a cluster being considered part of this t-shirt size class.
	Effects *Effects `json:"effects,omitempty"`

	// +kubebuilder:validation:Optional

	// Management configures the management aspects of this size class on the management plane.
	Management *Management `json:"management,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.to) || self.from <= self.to", message="lower limit must be less than or equal to the upper limit"

// NodeCountCriteria defines the criteria based on node count for a cluster to have a t-shirt size.
type NodeCountCriteria struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0

	// From is the inclusive lower limit to node count for a cluster to be considered a particular size.
	From uint32 `json:"from"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0

	// To is the inclusive upper limit to node count for a cluster to be considered a particular size.
	// If unset, this size class will match clusters of all sizes greater than the lower limit.
	To *uint32 `json:"to,omitempty"`
}

// Effects configures the effects on a cluster considered part of a t-shirt size class.
type Effects struct {

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^\d+(B|KiB|MiB|GiB|TiB)?$`
	// +kubebuilder:validation:MaxLength=20

	// KASGoMemLimit is the value to set for the $GOMEMLIMIT of the Kube APIServer container
	// Expected format is a number followed by a unit (B, KiB, MiB, GiB, TiB). See the Go runtime library for more
	// information on the format - https://pkg.go.dev/runtime#hdr-Environment_Variables.
	KASGoMemLimit *string `json:"kasGoMemLimit,omitempty"`

	// +kubebuilder:validation:Optional

	// ControlPlanePriorityClassName is the priority class to use for most control plane pods
	ControlPlanePriorityClassName *string `json:"controlPlanePriorityClassName,omitempty"`

	// +kubebuilder:validation:Optional

	// EtcdPriorityClassName is the priority class to use for etcd pods
	EtcdPriorityClassName *string `json:"etcdPriorityClassName,omitempty"`

	// +kubebuilder:validation:Optional

	// APICriticalPriorityClassName is the priority class for pods in the API request serving path.
	// This includes Kube API Server, OpenShift APIServer, etc.
	APICriticalPriorityClassName *string `json:"APICriticalPriorityClassName,omitempty"`

	// +kubebuilder:validation:Optional
	// ResourceRequests allows specifying resource requests for control plane pods.
	ResourceRequests []ResourceRequest `json:"resourceRequests,omitempty"`

	// +kubebuilder:validation:Optional
	// MachineHealthCheckTimeout specifies an optional timeout for machinehealthchecks created
	// for HostedClusters with this specific size.
	MachineHealthCheckTimeout *metav1.Duration `json:"machineHealthCheckTimeout,omitempty"`

	// +kubebuilder:validation:Optional
	// MaximumRequestsInFlight specifies the maximum requests in flight for Kube APIServer
	MaximumRequestsInflight *int `json:"maximumRequestsInflight,omitempty"`

	// +kubebuilder:validation:Optional
	// MaximumMutatingRequestsInflight specifies the maximum mutating requests in flight for Kube APIServer
	MaximumMutatingRequestsInflight *int `json:"maximumMutatingRequestsInflight,omitempty"`
}

// Management configures behaviors of the management plane for a size class.
type Management struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0

	// Placeholders configures the number of dummy workloads that will be scheduled irrespective of
	// HostedClusters in order to keep a set of nodes ready to accept new cluster creation and scheduling.
	Placeholders int `json:"placeholders,omitempty"`

	// +kubebuilder:validation:Optional

	// NonRequestServingNodesPerZone is the number of nodes to allocate for non request serving workloads
	// per HostedCluster. This will likely be a fraction of a node (ie. 0.2) to allow 5 HostedClusters in
	// a single node. The total number of nodes needed per HostedCluster is this number multiplied by 3
	// (number of zones).
	NonRequestServingNodesPerZone *resource.Quantity `json:"nonRequestServingNodesPerZone,omitempty"`
}

// ConcurrencyConfiguration defines bounds for the concurrency of clusters transitioning between states.
type ConcurrencyConfiguration struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default=`10m`

	// SlidingWindow is the window over which the concurrency bound is enforced.
	SlidingWindow metav1.Duration `json:"slidingWindow,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=5

	// Limit is the maximum allowed number of cluster size transitions during the sliding window.
	Limit int32 `json:"limit,omitempty"`
}

// TransitionDelayConfiguration defines the lag between cluster size changing and the assigned
// t-shirt size class being applied.
type TransitionDelayConfiguration struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default=`30s`

	// Increase defines the minimum period of time to wait between a cluster's size increasing and
	// the t-shirt size assigned to it being updated to reflect the new size.
	Increase metav1.Duration `json:"increase,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(s|m|h))+$`
	// +kubebuilder:default=`10m`

	// Decrease defines the minimum period of time to wait between a cluster's size decreasing and
	// the t-shirt size assigned to it being updated to reflect the new size.
	Decrease metav1.Duration `json:"decrease,omitempty"`
}

type ResourceRequest struct {
	// +kubebuilder:validation:Required
	// DeploymentName is the name of the deployment to which the resource request applies.
	DeploymentName string `json:"deploymentName"`

	// +kubebuilder:validation:Required
	// ContainerName is the name of the container to which the resource request applies.
	ContainerName string `json:"containerName"`

	// +kubebuilder:validation:Optional
	// Memory is the amount of memory to request for the container.
	Memory *resource.Quantity `json:"memory,omitempty"`

	// +kubebuilder:validation:Optional
	// CPU is the amount of CPU to request for the container.
	CPU *resource.Quantity `json:"cpu,omitempty"`
}

// ClusterSizingConfigurationStatus defines the observed state of ClusterSizingConfiguration
type ClusterSizingConfigurationStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge

	// Conditions contain details about the various aspects of cluster sizing.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

const (
	ClusterSizingConfigurationValidType = "ClusterSizingConfigurationValid"
)

// +kubebuilder:object:root=true

// ClusterSizingConfigurationList contains a list of ClusterSizingConfiguration.
type ClusterSizingConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterSizingConfiguration `json:"items"`
}
