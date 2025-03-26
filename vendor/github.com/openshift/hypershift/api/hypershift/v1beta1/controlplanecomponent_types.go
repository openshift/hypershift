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
	// ControlPlaneComponentProgressing indicates whether the ControlPlaneComponent is progressing.
	ControlPlaneComponentProgressing ConditionType = "Progressing"

	// WaitingForDependenciesReason indicates that there are unavailable dependencies blocking the ControlPlaneComponent reconciliation.
	WaitingForDependenciesReason string = "WaitingForDependencies"
	// ReconciliationErrorReason indicates that there was an error during the reconciliation of the ControlPlaneComponent.
	ReconciliationErrorReason string = "ReconciliationError"
)

// ControlPlaneComponentSpec defines the desired state of ControlPlaneComponent
type ControlPlaneComponentSpec struct {
}

// ComponentResource defines a resource reconciled by a ControlPlaneComponent.
type ComponentResource struct {
	// kind is the name of the resource schema.
	// +required
	Kind string `json:"kind"`

	// group is the API group for this resource type.
	// +required
	Group string `json:"group"`

	// name is the name of this resource.
	// +required
	Name string `json:"name"`
}

// ControlPlaneComponentStatus defines the observed state of ControlPlaneComponent
type ControlPlaneComponentStatus struct {
	// version reports the current version of this component.
	// +optional
	Version string `json:"version,omitempty"`

	// resources is a list of the resources reconciled by this component.
	// +optional
	Resources []ComponentResource `json:"resources,omitempty"`

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
// +kubebuilder:printcolumn:name="Progressing",type="string",JSONPath=".status.conditions[?(@.type==\"Progressing\")].status",description="Progressing"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].message",description="Message"
// +kubebuilder:printcolumn:name="ProgressingMessage",type="string",priority=1,JSONPath=".status.conditions[?(@.type==\"Progressing\")].message",description="ProgressingMessage"
// ControlPlaneComponent specifies the state of a ControlPlane Component
// +openshift:enable:FeatureGate=ControlPlaneV2
type ControlPlaneComponent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ControlPlaneComponentSpec   `json:"spec,omitempty"`
	Status ControlPlaneComponentStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ControlPlaneComponentList contains a list of ControlPlaneComponent
type ControlPlaneComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControlPlaneComponent `json:"items"`
}
