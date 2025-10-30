package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&MachineAutoscaler{}, &MachineAutoscalerList{})
}

// MachineAutoscalerSpec defines the desired state of MachineAutoscaler
type MachineAutoscalerSpec struct {
	// MinReplicas constrains the minimal number of replicas of a scalable resource
	// +kubebuilder:validation:Minimum=0
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas constrains the maximal number of replicas of a scalable resource
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// ScaleTargetRef holds reference to a scalable resource
	ScaleTargetRef CrossVersionObjectReference `json:"scaleTargetRef"`
}

// MachineAutoscalerStatus defines the observed state of MachineAutoscaler
type MachineAutoscalerStatus struct {
	// LastTargetRef holds reference to the recently observed scalable resource
	LastTargetRef *CrossVersionObjectReference `json:"lastTargetRef,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MachineAutoscaler is the Schema for the machineautoscalers API
// +k8s:openapi-gen=true
// +kubebuilder:resource:path=machineautoscalers,shortName=ma,scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ref Kind",type="string",JSONPath=".spec.scaleTargetRef.kind",description="Kind of object scaled"
// +kubebuilder:printcolumn:name="Ref Name",type="string",JSONPath=".spec.scaleTargetRef.name",description="Name of object scaled"
// +kubebuilder:printcolumn:name="Min",type="integer",JSONPath=".spec.minReplicas",description="Min number of replicas"
// +kubebuilder:printcolumn:name="Max",type="integer",JSONPath=".spec.maxReplicas",description="Max number of replicas"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="MachineAutoscaler resoruce age"
type MachineAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of constraints of a scalable resource
	Spec MachineAutoscalerSpec `json:"spec,omitempty"`

	// Most recently observed status of a scalable resource
	Status MachineAutoscalerStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MachineAutoscalerList contains a list of MachineAutoscaler
type MachineAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MachineAutoscaler `json:"items"`
}

// CrossVersionObjectReference identifies another object by name, API version,
// and kind.
type CrossVersionObjectReference struct {
	// APIVersion defines the versioned schema of this representation of an
	// object. Servers should convert recognized schemas to the latest internal
	// value, and may reject unrecognized values. More info:
	// https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind is a string value representing the REST resource this object
	// represents. Servers may infer this from the endpoint the client submits
	// requests to. Cannot be updated. In CamelCase. More info:
	// https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name specifies a name of an object, e.g. worker-us-east-1a.
	// Scalable resources are expected to exist under a single namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}
