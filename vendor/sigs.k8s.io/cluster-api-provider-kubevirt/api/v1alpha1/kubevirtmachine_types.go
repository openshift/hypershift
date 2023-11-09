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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/errors"
)

const (
	// MachineFinalizer allows ReconcileKubevirtMachine to clean up resources associated with machine before
	// removing it from the apiserver.
	MachineFinalizer = "kubevirtmachine.infrastructure.cluster.x-k8s.io"
)

// VirtualMachineTemplateSpec defines the desired state of the kubevirt VM.
type VirtualMachineTemplateSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	// VirtualMachineSpec contains the VirtualMachine specification.
	Spec kubevirtv1.VirtualMachineSpec `json:"spec,omitempty" valid:"required"`
}

// KubevirtMachineSpec defines the desired state of KubevirtMachine.
type KubevirtMachineSpec struct {
	VirtualMachineTemplate VirtualMachineTemplateSpec `json:"virtualMachineTemplate,omitempty"`

	// ProviderID TBD what to use for Kubevirt
	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// InfraClusterSecretRef is a reference to a secret with a kubeconfig for external cluster used for infra.
	// When nil, this defaults to the value present in the KubevirtCluster object's spec associated with this machine.
	// +optional
	InfraClusterSecretRef *corev1.ObjectReference `json:"infraClusterSecretRef,omitempty"`
}

// KubevirtMachineStatus defines the observed state of KubevirtMachine.
type KubevirtMachineStatus struct {
	// Ready denotes that the machine is ready
	// +optional
	Ready bool `json:"ready"`

	// LoadBalancerConfigured denotes that the machine has been
	// added to the load balancer
	// +optional
	LoadBalancerConfigured bool `json:"loadBalancerConfigured,omitempty"`

	// Addresses contains the associated addresses for the machine.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// Conditions defines current service state of the KubevirtMachine.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// NodeUpdated denotes that the ProviderID is updated on Node of this KubevirtMachine
	// +optional
	NodeUpdated bool `json:"nodeupdated"`

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
}

// +kubebuilder:resource:path=kubevirtmachines,scope=Namespaced,categories=cluster-api
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

// KubevirtMachine is the Schema for the kubevirtmachines API.
type KubevirtMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubevirtMachineSpec   `json:"spec,omitempty"`
	Status KubevirtMachineStatus `json:"status,omitempty"`
}

func (c *KubevirtMachine) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

func (c *KubevirtMachine) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// KubevirtMachineList contains a list of KubevirtMachine.
type KubevirtMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubevirtMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubevirtMachine{}, &KubevirtMachineList{})
}
