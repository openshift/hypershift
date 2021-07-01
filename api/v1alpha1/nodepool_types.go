package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NodePoolAMIDiscoveryFailed              = "AMIDiscoveryFailed"
	NodePoolAutoscalingEnabledConditionType = "AutoscalingEnabled"
	NodePoolAutorepairEnabledConditionType  = "AutorepairEnabled"
	NodePoolAsExpectedConditionReason       = "AsExpected"
	NodePoolValidationFailedConditionReason = "ValidationFailed"
	NodePoolUpgradingConditionType          = "Upgrading"
)

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	IgnitionEndpointMissingReason string = "IgnitionEndpointMissing"
	IgnitionCACertMissingReason   string = "IgnitionCACertMissing"
	IgnitionTokenMissingError     string = "IgnitionTokenError"
	IgnitionUserDataErrorReason   string = "IgnitionUserDataError"
)

func init() {
	SchemeBuilder.Register(&NodePool{})
	SchemeBuilder.Register(&NodePoolList{})
}

// NodePool defines the desired state of NodePool
// +kubebuilder:resource:path=nodepools,shortName=np;nps,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true
// +kubebuilder:subresource:scale:specpath=.spec.nodeCount,statuspath=.status.nodeCount
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterName",description="Cluster"
// +kubebuilder:printcolumn:name="NodeCount",type="integer",JSONPath=".status.nodeCount",description="Available Nodes"
// +kubebuilder:printcolumn:name="Autoscaling",type="string",JSONPath=".status.conditions[?(@.type==\"AutoscalingEnabled\")].status",description="Autoscaling Enabled"
// +kubebuilder:printcolumn:name="Autorepair",type="string",JSONPath=".status.conditions[?(@.type==\"AutorepairEnabled\")].status",description="Node Autorepair Enabled"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="Current version"
// +kubebuilder:printcolumn:name="Upgrading",type="string",JSONPath=".status.conditions[?(@.type==\"Upgrading\")].status",description="Upgrade in progress"
type NodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodePoolSpec   `json:"spec,omitempty"`
	Status NodePoolStatus `json:"status,omitempty"`
}

// NodePoolSpec defines the desired state of NodePool
type NodePoolSpec struct {
	// ClusterName is the name of the Cluster this object belongs to.
	ClusterName string `json:"clusterName"`
	// +optional
	NodeCount *int32 `json:"nodeCount"`
	// +optional
	AutoScaling *NodePoolAutoScaling `json:"autoScaling,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={maxSurge: 1, maxUnavailable: 0, autoRepair: false}
	Management NodePoolManagement `json:"nodePoolManagement"`
	Platform   NodePoolPlatform   `json:"platform"`

	// Release specifies the release image to use for this NodePool
	// For a nodePool a given version dictates the ignition config and
	// an image artifact e.g an AMI in AWS.
	// Release specifies the release image to use for this HostedCluster
	// +kubebuilder:validation:Required
	// +required
	Release Release `json:"release"`
}

// NodePoolStatus defines the observed state of NodePool
type NodePoolStatus struct {
	// NodeCount is the most recently observed number of replicas.
	// +optional
	NodeCount  int                `json:"nodeCount"`
	Conditions []metav1.Condition `json:"conditions"`

	// Version is the semantic version of the release applied by
	// the hosted control plane operator.
	// For a nodePool a given version represents the ignition config and
	// an image artifact e.g an AMI in AWS.
	// +kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`
}

// +kubebuilder:object:root=true
// NodePoolList contains a list of NodePools.
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodePool `json:"items"`
}

type NodePoolManagement struct {
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	MaxUnavailable int `json:"maxUnavailable"`
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	MaxSurge int `json:"maxSurge"`

	// +optional
	AutoRepair bool `json:"autoRepair"`
}

type NodePoolAutoScaling struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	Min *int `json:"min"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	Max *int `json:"max"`
}

// NodePoolPlatform is the platform-specific configuration for a node
// pool. Only one of the platforms should be set.
type NodePoolPlatform struct {
	Type PlatformType `json:"type"`
	// AWS is the configuration used when installing on AWS.
	AWS *AWSNodePoolPlatform `json:"aws,omitempty"`
}

// AWSNodePoolPlatform stores the configuration for a node pool
// installed on AWS.
type AWSNodePoolPlatform struct {
	// InstanceType defines the ec2 instance type.
	// eg. m4-large
	InstanceType    string `json:"instanceType"`
	InstanceProfile string `json:"instanceProfile,omitempty"`
	// Subnet is the subnet to use for instances
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`
	// AMI is the image id to use
	// +optional
	AMI string `json:"ami,omitempty"`
	// SecurityGroups is the set of security groups to associate with nodepool machines
	// +optional
	SecurityGroups []AWSResourceReference `json:"securityGroups,omitempty"`
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
