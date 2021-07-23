package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	NodePoolValidReleaseImageConditionType       = "ValidReleaseImage"
	NodePoolValidAMIConditionType                = "ValidAMI"
	NodePoolConfigValidConfigConditionType       = "ValidConfig"
	NodePoolUpdateManagementEnabledConditionType = "UpdateManagementEnabled"
	NodePoolAutoscalingEnabledConditionType      = "AutoscalingEnabled"
	NodePoolAutorepairEnabledConditionType       = "AutorepairEnabled"
	NodePoolUpdatingVersionConditionType         = "UpdatingVersion"
	NodePoolUpdatingConfigConditionType          = "UpdatingConfig"
	NodePoolAsExpectedConditionReason            = "AsExpected"
	NodePoolValidationFailedConditionReason      = "ValidationFailed"
)

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	IgnitionEndpointMissingReason string = "IgnitionEndpointMissing"
	IgnitionCACertMissingReason   string = "IgnitionCACertMissing"
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
// +kubebuilder:printcolumn:name="UpdatingVersion",type="string",JSONPath=".status.conditions[?(@.type==\"UpdatingVersion\")].status",description="UpdatingVersion in progress"
// +kubebuilder:printcolumn:name="UpdatingConfig",type="string",JSONPath=".status.conditions[?(@.type==\"UpdatingConfig\")].status",description="UpdatingConfig in progress"
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

	// +kubebuilder:validation:Optional
	// TODO (alberto): this ConfigMaps are meant to contain
	// MachineConfig, KubeletConfig and ContainerRuntimeConfig but
	// MCO only supports MachineConfig in bootstrap mode atm
	// https://github.com/openshift/machine-config-operator/blob/9c6c2bfd7ed498bfbc296d530d1839bd6a177b0b/pkg/controller/bootstrap/bootstrap.go#L104-L119
	// By contractual convention the ConfigMap structure is as follow:
	// type: ConfigMap
	//   data:
	//     config: |-
	Config []v1.LocalObjectReference `json:"config,omitempty"`

	Management NodePoolManagement `json:"nodePoolManagement"`

	// +optional
	AutoScaling *NodePoolAutoScaling `json:"autoScaling,omitempty"`

	Platform NodePoolPlatform `json:"platform"`

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
	NodeCount int32 `json:"nodeCount"`

	// TODO (alberto): store list of existing nodes?
	// Nodes []corev1.Nodes

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

type UpgradeType string

const UpgradeTypeInPlace = UpgradeType("InPlace")
const UpgradeTypeReplace = UpgradeType("Replace")

type UpgradeStrategy string

const UpgradeStrategyRollingUpdate = UpgradeStrategy("RollingUpdate")
const UpgradeStrategyOnDelete = UpgradeStrategy("OnDelete")

type ReplaceUpgrade struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=RollingUpdate;OnDelete
	Strategy UpgradeStrategy `json:"strategy"`
	// +kubebuilder:validation:Optional
	RollingUpdate *RollingUpdate `json:"rollingUpdate,omitempty"`
}

type RollingUpdate struct {
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	MaxSurge       *intstr.IntOrString `json:"maxSurge,omitempty"`
}

type InPlaceUpgrade struct {
}

type NodePoolManagement struct {
	// +kubebuilder:validation:Enum=Replace;InPlace
	UpgradeType UpgradeType `json:"upgradeType"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={strategy: "RollingUpdate", rollingUpdate: {maxSurge: 1, maxUnavailable: 0 }}
	Replace *ReplaceUpgrade `json:"recreate,omitempty"`
	// +kubebuilder:validation:Optional
	InPlace *InPlaceUpgrade `json:"inPlace,omitempty"`

	// +optional
	AutoRepair bool `json:"autoRepair"`
}

type NodePoolAutoScaling struct {
	// +kubebuilder:validation:Minimum=1
	Min int32 `json:"min"`
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
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
