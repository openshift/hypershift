/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// IBMPowerVSMachineTemplateSpec defines the desired state of IBMPowerVSMachineTemplate.
type IBMPowerVSMachineTemplateSpec struct {
	Template IBMPowerVSMachineTemplateResource `json:"template"`
}

// IBMPowerVSMachineTemplateResource holds the IBMPowerVSMachine spec.
type IBMPowerVSMachineTemplateResource struct {
	Spec IBMPowerVSMachineSpec `json:"spec"`
}

// IBMPowerVSMachineTemplateStatus defines the observed state of IBMPowerVSMachineTemplate.
type IBMPowerVSMachineTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// IBMPowerVSMachineTemplate is the Schema for the ibmpowervsmachinetemplates API.
type IBMPowerVSMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IBMPowerVSMachineTemplateSpec   `json:"spec,omitempty"`
	Status IBMPowerVSMachineTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IBMPowerVSMachineTemplateList contains a list of IBMPowerVSMachineTemplate.
type IBMPowerVSMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IBMPowerVSMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IBMPowerVSMachineTemplate{}, &IBMPowerVSMachineTemplateList{})
}
