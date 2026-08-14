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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubevirtMachineTemplateSpec defines the desired state of KubevirtMachineTemplate.
type KubevirtMachineTemplateSpec struct {
	Template KubevirtMachineTemplateResource `json:"template"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=kubevirtmachinetemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion

// KubevirtMachineTemplate is the Schema for the kubevirtmachinetemplates API.
type KubevirtMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec KubevirtMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// KubevirtMachineTemplateList contains a list of KubevirtMachineTemplate.
type KubevirtMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubevirtMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubevirtMachineTemplate{}, &KubevirtMachineTemplateList{})
}

// KubevirtMachineTemplateResource describes the data needed to create a KubevirtMachine from a template.
type KubevirtMachineTemplateResource struct {
	// Spec is the specification of the desired behavior of the machine.
	Spec KubevirtMachineSpec `json:"spec"`
}
