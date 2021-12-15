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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// IBMPowerVSMachineFinalizer allows IBMPowerVSMachineReconciler to clean up resources associated with IBMPowerVSMachine before
	// removing it from the apiserver.
	IBMPowerVSMachineFinalizer = "ibmpowervsmachine.infrastructure.cluster.x-k8s.io"
)

// IBMPowerVSMachineSpec defines the desired state of IBMPowerVSMachine
type IBMPowerVSMachineSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ServiceInstanceID is the id of the power cloud instance where the vsi instance will get deployed
	ServiceInstanceID string `json:"serviceInstanceID"`

	// SSHKey is the name of the SSH key pair provided to the vsi for authenticating users
	SSHKey string `json:"sshKey,omitempty"`

	// Image is the reference to the Image from which to create the machine instance.
	Image IBMPowerVSResourceReference `json:"image"`

	// SysType is the System type used to host the vsi
	SysType string `json:"sysType"`

	// ProcType is the processor type, e.g: dedicated, shared, capped
	ProcType string `json:"procType"`

	// Processors is Number of processors allocated
	Processors string `json:"processors"`

	// Memory is Amount of memory allocated (in GB)
	Memory string `json:"memory"`

	// Network is the reference to the Network to use for this instance.
	Network IBMPowerVSResourceReference `json:"network"`

	// ProviderID is the unique identifier as specified by the cloud provider.
	// +optional
	ProviderID *string `json:"providerID,omitempty"`
}

// IBMPowerVSResourceReference is a reference to a specific PowerVS resource by ID or Name
// Only one of ID or Name may be specified. Specifying more than one will result in
// a validation error.
type IBMPowerVSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// Name of resource
	// +optional
	Name *string `json:"name,omitempty"`
}

// IBMPowerVSMachineStatus defines the observed state of IBMPowerVSMachine
type IBMPowerVSMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	InstanceID string `json:"instanceID,omitempty"`

	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// Addresses contains the vsi associated addresses.
	Addresses []v1.NodeAddress `json:"addresses,omitempty"`

	// Health is the health of the vsi
	// +optional
	Health string `json:"health,omitempty"`

	// InstanceState is the status of the vsi
	InstanceState string `json:"instanceState"`

	// Fault will report if any fault messages for the vsi
	// +optional
	Fault string `json:"fault,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this IBMPowerVSMachine belongs"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Cluster infrastructure is ready for IBM PowerVS instances"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.instanceState",description="PowerVS instance state"
// +kubebuilder:printcolumn:name="Health",type="string",JSONPath=".status.health",description="PowerVS instance health"

// IBMPowerVSMachine is the Schema for the ibmpowervsmachines API
type IBMPowerVSMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IBMPowerVSMachineSpec   `json:"spec,omitempty"`
	Status IBMPowerVSMachineStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IBMPowerVSMachineList contains a list of IBMPowerVSMachine
type IBMPowerVSMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IBMPowerVSMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IBMPowerVSMachine{}, &IBMPowerVSMachineList{})
}
