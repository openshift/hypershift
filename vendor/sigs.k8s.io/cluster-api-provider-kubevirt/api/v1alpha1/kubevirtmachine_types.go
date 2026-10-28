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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/errors" //nolint:staticcheck // deprecated but no replacement yet
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

	// BootstrapCheckSpec defines how the CAPK controller is checking CAPI Sentinel file inside the VM.
	// +optional
	BootstrapCheckSpec VirtualMachineBootstrapCheckSpec `json:"virtualMachineBootstrapCheck,omitempty"`

	// InfraClusterSecretRef is a reference to a secret with a kubeconfig for external cluster used for infra.
	// When nil, this defaults to the value present in the KubevirtCluster object's spec associated with this machine.
	// +optional
	InfraClusterSecretRef *corev1.ObjectReference `json:"infraClusterSecretRef,omitempty"`
}

// VirtualMachineBootstrapCheckSpec defines how the controller will remotely check CAPI Sentinel file content.
type VirtualMachineBootstrapCheckSpec struct {
	// CheckStrategy describes how CAPK controller will validate a successful CAPI bootstrap.
	// Following specified method, CAPK will try to retrieve the state of the CAPI Sentinel file from the VM.
	// Possible values are: "none" or "ssh" (default is "ssh") and this value is validated by apiserver.
	// +optional
	// +kubebuilder:validation:Enum=none;ssh
	// +kubebuilder:default:=ssh
	CheckStrategy string `json:"checkStrategy,omitempty"`
}

// KubevirtMachineInitializationStatus tracks CAPI v1beta2 contract fields.
type KubevirtMachineInitializationStatus struct {
	// Provisioned indicates that the infrastructure has been provisioned.
	// Required by CAPI v1beta2 Machine controller to proceed with providerID propagation.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// KubevirtMachineStatus defines the observed state of KubevirtMachine.
type KubevirtMachineStatus struct {
	// Ready denotes that the machine is ready
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Initialization tracks CAPI v1beta2 contract initialization fields.
	// +optional
	Initialization *KubevirtMachineInitializationStatus `json:"initialization,omitempty"`

	// LoadBalancerConfigured denotes that the machine has been
	// added to the load balancer
	// +optional
	LoadBalancerConfigured bool `json:"loadBalancerConfigured,omitempty"`

	// Addresses contains the associated addresses for the machine.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// Conditions defines current service state of the KubevirtMachine.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

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
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready",description="Is machine ready"

// KubevirtMachine is the Schema for the kubevirtmachines API.
type KubevirtMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubevirtMachineSpec   `json:"spec,omitempty"`
	Status KubevirtMachineStatus `json:"status,omitempty"`
}

func (c *KubevirtMachine) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

func (c *KubevirtMachine) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// KubevirtMachineList contains a list of KubevirtMachine.
type KubevirtMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubevirtMachine `json:"items"`
}

