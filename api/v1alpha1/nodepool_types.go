package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NodePoolAutoscalingEnabledConditionType = "AutoscalingEnabled"
	NodePoolAsExpectedConditionReason       = "AsExpected"
	NodePoolValidationFailedConditionReason = "ValidationFailed"
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
// +kubebuilder:printcolumn:name="NodeCount",type="integer",JSONPath=".status.nodeCount",description="Available Nodes"
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
	NodeCount   *int32              `json:"nodeCount"`
	AutoScaling NodePoolAutoScaling `json:"autoScaling,omitempty"`
	Platform    NodePoolPlatform    `json:"platform"`
}

// NodePoolStatus defines the observed state of NodePool
type NodePoolStatus struct {
	// NodeCount is the most recently observed number of replicas.
	// +optional
	NodeCount  int                `json:"nodeCount"`
	Conditions []metav1.Condition `json:"conditions"`
}

// +kubebuilder:object:root=true
// NodePoolList contains a list of NodePools.
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodePool `json:"items"`
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
	// AWS is the configuration used when installing on AWS.
	AWS *AWSNodePoolPlatform `json:"aws,omitempty"`
}

// AWSNodePoolPlatform stores the configuration for a node pool
// installed on AWS.
type AWSNodePoolPlatform struct {
	// InstanceType defines the ec2 instance type.
	// eg. m4-large
	InstanceType    string                `json:"instanceType"`
	InstanceProfile string                `json:"instanceProfile,omitempty"`
	Subnet          *AWSResourceReference `json:"subnet,omitempty"`
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
