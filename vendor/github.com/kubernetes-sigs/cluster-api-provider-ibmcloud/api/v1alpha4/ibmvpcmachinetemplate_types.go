package v1alpha4

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IBMVPCMachineTemplateSpec defines the desired state of IBMVPCMachineTemplate
type IBMVPCMachineTemplateSpec struct {
	Template IBMVPCMachineTemplateResource `json:"template"`
}

// IBMVPCMachineTemplateResource describes the data needed to create am IBMVPCMachine from a template
type IBMVPCMachineTemplateResource struct {
	// Spec is the specification of the desired behavior of the machine.
	Spec IBMVPCMachineSpec `json:"spec"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ibmvpcmachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion

// IBMVPCMachineTemplate is the Schema for the IBMVPCMachinetemplates API
type IBMVPCMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec IBMVPCMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// IBMVPCMachineTemplateList contains a list of IBMVPCMachineTemplate
type IBMVPCMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IBMVPCMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IBMVPCMachineTemplate{}, &IBMVPCMachineTemplateList{})
}
