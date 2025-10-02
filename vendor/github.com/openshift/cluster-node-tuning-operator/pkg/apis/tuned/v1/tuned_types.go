package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

const (
	// TunedDefaultResourceName is the name of the Node Tuning Operator's default custom tuned resource.
	TunedDefaultResourceName = "default"

	// TunedRenderedResourceName is the name of the Node Tuning Operator's tuned resource combined out of
	// all the other custom tuned resources.
	TunedRenderedResourceName = "rendered"

	// TunedClusterOperatorResourceName is the name of the clusteroperator resource
	// that reflects the node tuning operator status.
	TunedClusterOperatorResourceName = "node-tuning"

	// Name of the NTO operand for versioning in ClusterOperator.
	TunedOperandName = "ocp-tuned"

	// TunedBootcmdlineAnnotationKey is a Node-specific annotation denoting kernel command-line parameters
	// calculated by TuneD for the current profile applied to that Node.
	TunedBootcmdlineAnnotationKey string = "tuned.openshift.io/bootcmdline"

	// TunedDeferredUpdate request the tuned daemons to defer the update of the rendered profile
	// until the next restart.
	TunedDeferredUpdate string = "tuned.openshift.io/deferred"
)

/////////////////////////////////////////////////////////////////////////////////
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

// Tuned is a collection of rules that allows cluster-wide deployment
// of node-level sysctls and more flexibility to add custom tuning
// specified by user needs.  These rules are translated and passed to all
// containerized Tuned daemons running in the cluster in the format that
// the daemons understand. The responsibility for applying the node-level
// tuning then lies with the containerized Tuned daemons. More info:
// https://github.com/openshift/cluster-node-tuning-operator
type Tuned struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the desired behavior of Tuned. More info:
	// https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status
	Spec   TunedSpec   `json:"spec,omitempty"`
	Status TunedStatus `json:"status,omitempty"`
}

type TunedSpec struct {
	// managementState indicates whether the registry instance represented
	// by this config instance is under operator management or not.  Valid
	// values are Force, Managed, Unmanaged, and Removed.
	// +optional
	ManagementState operatorv1.ManagementState `json:"managementState,omitempty" protobuf:"bytes,1,opt,name=managementState,casttype=github.com/openshift/api/operator/v1.ManagementState"`
	// Tuned profiles.
	// +optional
	Profile []TunedProfile `json:"profile"`
	// Selection logic for all Tuned profiles.
	// +optional
	Recommend []TunedRecommend `json:"recommend"`
}

// A Tuned profile.
type TunedProfile struct {
	// Name of the Tuned profile to be used in the recommend section.
	// +kubebuilder:validation:MinLength=1
	Name *string `json:"name"`
	// Specification of the Tuned profile to be consumed by the Tuned daemon.
	Data *string `json:"data"`
}

// Selection logic for a single Tuned profile.
type TunedRecommend struct {
	// Name of the Tuned profile to recommend.
	// +kubebuilder:validation:MinLength=1
	Profile *string `json:"profile"`

	// Tuned profile priority. Highest priority is 0.
	// +kubebuilder:validation:Minimum=0
	Priority *uint64 `json:"priority"`
	// Rules governing application of a Tuned profile connected by logical OR operator.
	Match []TunedMatch `json:"match,omitempty"`
	// MachineConfigLabels specifies the labels for a MachineConfig. The MachineConfig is created
	// automatically to apply additional host settings (e.g. kernel boot parameters) profile 'Profile'
	// needs and can only be applied by creating a MachineConfig. This involves finding all
	// MachineConfigPools with machineConfigSelector matching the MachineConfigLabels and setting the
	// profile 'Profile' on all nodes that match the MachineConfigPools' nodeSelectors.
	MachineConfigLabels map[string]string `json:"machineConfigLabels,omitempty"`

	// Optional operand configuration.
	// +optional
	Operand OperandConfig `json:"operand,omitempty"`
}

// Rules governing application of a Tuned profile.
type TunedMatch struct {
	// Node or Pod label name.
	Label *string `json:"label"`
	// Node or Pod label value. If omitted, the presence of label name is enough to match.
	Value *string `json:"value,omitempty"`
	// Match type: [node/pod]. If omitted, "node" is assumed.
	// +kubebuilder:validation:Enum={"node","pod"}
	Type *string `json:"type,omitempty"`

	// Additional rules governing application of the tuned profile connected by logical AND operator.
	Match []TunedMatch `json:"match,omitempty"`
}

type OperandConfig struct {
	// turn debugging on/off for the TuneD daemon: true/false (default is false)
	// +optional
	Debug bool `json:"debug,omitempty"`

	// klog logging verbosity
	// +optional
	Verbosity int `json:"verbosity,omitempty"`

	// +optional
	TuneDConfig TuneDConfig `json:"tunedConfig,omitempty"`
}

// Global configuration for the TuneD daemon as defined in tuned-main.conf
type TuneDConfig struct {
	// turn reapply_sysctl functionality on/off for the TuneD daemon: true/false
	// +optional
	ReapplySysctl *bool `json:"reapply_sysctl"`
}

// TunedStatus is the status for a Tuned resource.
type TunedStatus struct {
	// conditions represents the state of the Tuned profile
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +optional
	Conditions []StatusCondition `json:"conditions,omitempty"  patchStrategy:"merge" patchMergeKey:"type"`
}

const (
	// Tuned CR was validated and no problems with it were found.
	TunedValid ConditionType = "Valid"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TunedList is a list of Tuned resources.
type TunedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tuned `json:"items"`
}

/////////////////////////////////////////////////////////////////////////////////
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status

// Profile is a specification for a Profile resource.
type Profile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProfileSpec   `json:"spec,omitempty"`
	Status ProfileStatus `json:"status,omitempty"`
}

type ProfileSpec struct {
	Config ProfileConfig `json:"config"`
	// Tuned profiles.
	// +optional
	Profile []TunedProfile `json:"profile"`
}

type ProfileConfig struct {
	// TuneD profile to apply
	TunedProfile string `json:"tunedProfile"`
	// option to debug TuneD daemon execution
	// +optional
	Debug bool `json:"debug"`
	// klog logging verbosity
	// +optional
	Verbosity int `json:"verbosity"`
	// +optional
	TuneDConfig TuneDConfig `json:"tunedConfig,omitempty"`
	// Name of the cloud provider as taken from the Node providerID: <ProviderName>://<ProviderSpecificNodeID>
	// +optional
	ProviderName string `json:"providerName,omitempty"`
}

// ProfileStatus is the status for a Profile resource; the status is for internal use only
// and its fields may be changed/removed in the future.
type ProfileStatus struct {
	// the current profile in use by the Tuned daemon
	TunedProfile string `json:"tunedProfile"`

	// conditions represents the state of the per-node Profile application
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +optional
	Conditions []StatusCondition `json:"conditions,omitempty"  patchStrategy:"merge" patchMergeKey:"type"`

	// If set, this represents the .metadata.generation that the conditions were set based upon.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,1,opt,name=observedGeneration"`
}

// StatusCondition represents a partial state of the per-node Profile application.
// +k8s:deepcopy-gen=true
type StatusCondition struct {
	// type specifies the aspect reported by this condition.
	// +kubebuilder:validation:Required
	// +required
	Type ConditionType `json:"type"`

	// status of the condition, one of True, False, Unknown.
	// +kubebuilder:validation:Required
	// +required
	Status corev1.ConditionStatus `json:"status"`

	// lastTransitionTime is the time of the last update to the current status property.
	// +kubebuilder:validation:Required
	// +required
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`

	// reason is the CamelCase reason for the condition's current status.
	// +optional
	Reason string `json:"reason,omitempty"`

	// message provides additional information about the current condition.
	// This is only to be consumed by humans.
	// +optional
	Message string `json:"message,omitempty"`
}

// ConditionType is an aspect of Tuned daemon profile application state.
type ConditionType string

const (
	// ProfileApplied indicates that the Tuned daemon has successfully applied
	// the selected profile.
	TunedProfileApplied ConditionType = "Applied"

	// TunedDegraded indicates the Tuned daemon issued errors during profile
	// application.  To conclude the profile application was successful,
	// both TunedProfileApplied and TunedDegraded need to be queried.
	TunedDegraded ConditionType = "Degraded"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ProfileList is a list of Profile resources.
type ProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Profile `json:"items"`
}
