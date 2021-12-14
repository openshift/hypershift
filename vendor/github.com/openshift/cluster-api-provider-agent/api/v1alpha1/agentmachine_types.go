/*
Copyright 2021.

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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/errors"
)

// AgentMachineSpec defines the desired state of AgentMachine
type AgentMachineSpec struct {
	// The minimum number of CPU cores that this Machine requires
	// +optional
	MinCPUs int32 `json:"minCPUs,omitempty"`

	// The minimum amount of RAM that this Machine requires, in MiB
	// +optional
	MinMemoryMiB int32 `json:"minMemoryMiB,omitempty"`

	// The labels that must be set on an Agent in order to be selected for this Machine
	// +optional
	AgentLabelSelector *metav1.LabelSelector `json:"agentLabelSelector,omitempty"`

	// ProviderID is the host's motherboard serial formatted as
	// agent://12345678-1234-1234-1234-123456789abc
	ProviderID *string `json:"providerID,omitempty"`
}

// AgentMachineStatus defines the observed state of AgentMachine
type AgentMachineStatus struct {
	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// The Agent matched to the Machine
	AgentRef *AgentReference `json:"agentRef,omitempty"`

	// Addresses contains the Agent's associated addresses.
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// FailureReason will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a succinct value suitable
	// for machine interpretation.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the Machine's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	FailureReason *errors.MachineStatusError `json:"failureReason,omitempty"`

	// FailureMessage will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a more verbose string suitable
	// for logging and human consumption.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the Machine's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the AgentMachine.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AgentMachine is the Schema for the agentmachines API
type AgentMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentMachineSpec   `json:"spec,omitempty"`
	Status AgentMachineStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AgentMachineList contains a list of AgentMachine
type AgentMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentMachine{}, &AgentMachineList{})
}
