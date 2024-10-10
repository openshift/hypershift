package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&ControlPlaneComponent{},
			&ControlPlaneComponentList{},
		)
		return nil
	})
}

const (
	// ControlPlaneComponentAvailable indicates whether the ControlPlaneComponent is available.
	ControlPlaneComponentAvailable ConditionType = "Available"

	// WaitingForDependenciesReason indicates that there are unavailable dependencies blocking the ControlPlaneComponent reconcilation.
	WaitingForDependenciesReason string = "WaitingForDependencies"
	// ReconciliationErrorReason indicates that there was an error during the reconcilation of the ControlPlaneComponent.
	ReconciliationErrorReason string = "ReconciliationError"
)

// ControlPlaneComponentSpec defines the desired state of ControlPlaneComponent
type ControlPlaneComponentSpec struct {
}

type ComponentResource struct {
	Kind string `json:"kind"`

	Group string `json:"group"`

	Name string `json:"name"`
}

// ControlPlaneComponentStatus defines the observed state of ControlPlaneComponent
type ControlPlaneComponentStatus struct {
	// Resources is a list of the resources reconciled by this component.
	// +optional
	Resources []ComponentResource `json:"resources,omitempty"`

	// Version reports the current version of this component.
	Version string `json:"version,omitempty"`

	// Conditions contains details for the current state of the ControlPlane Component.
	// If there is an error, then the Available condition will be false.
	//
	// Current condition types are: "Available"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=controlplanecomponents,shortName=cpc;cpcs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="Version"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].message",description="Message"
// ControlPlaneComponent specifies the state of a ControlPlane Component
type ControlPlaneComponent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ControlPlaneComponentSpec   `json:"spec,omitempty"`
	Status ControlPlaneComponentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// ControlPlaneComponentList contains a list of ControlPlaneComponent
type ControlPlaneComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControlPlaneComponent `json:"items"`
}
