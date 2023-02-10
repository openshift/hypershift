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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// ClusterFinalizer allows KubevirtClusterReconciler to clean up resources associated with KubevirtCluster before
	// removing it from the apiserver.
	ClusterFinalizer = "kubevirtcluster.infrastructure.cluster.x-k8s.io"
)

const ( //labels
	KubevirtMachineNameLabel      = "capk.cluster.x-k8s.io/kubevirt-machine-name"
	KubevirtMachineNamespaceLabel = "capk.cluster.x-k8s.io/kubevirt-machine-namespace"

	KubevirtMachineVMTerminalLabel = "capk.cluster.x-k8s.io/vm-is-terminal"
)

const ( // annotations
	VmiDeletionGraceTime = "capk.cluster.x-k8s.io/vmi-deletion-grace-time"
)

// KubevirtClusterSpec defines the desired state of KubevirtCluster.
type KubevirtClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// ControlPlaneServiceTemplate can be used to modify service that fronts the control plane nodes to handle the
	// api-server traffic (port 6443). This field is optional, by default control plane nodes will use a service
	// of type ClusterIP, which will make workload cluster only accessible within the same cluster. Note, this does
	// not aim to expose the entire Service spec to users, but only provides capability to modify the service metadata
	// and the service type.
	// +optional
	ControlPlaneServiceTemplate ControlPlaneServiceTemplate `json:"controlPlaneServiceTemplate,omitempty"`

	// SSHKeys is a reference to a local struct for SSH keys persistence.
	// +optional
	SshKeys SSHKeys `json:"sshKeys,omitempty"`

	// InfraClusterSecretRef is a reference to a secret with a kubeconfig for external cluster used for infra.
	// +optional
	InfraClusterSecretRef *corev1.ObjectReference `json:"infraClusterSecretRef,omitempty"`
}

// KubevirtClusterStatus defines the observed state of KubevirtCluster.
type KubevirtClusterStatus struct {
	// Ready denotes that the infrastructure is ready.
	Ready bool `json:"ready"`

	// FailureDomains don't mean much in CAPD since it's all local, but we can see how the rest of cluster API
	// will use this if we populate it.
	FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`

	// Conditions defines current service state of the KubevirtCluster.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int `json:"port"`
}

// SSHKeys encapsulates fields for SSH keys persistence.
type SSHKeys struct {
	// ConfigRef is a reference to a resource containing the keys.
	// The reference is optional to allow users/operators to specify
	// Bootstrap.DataSecretName without the need of a controller.
	// +optional
	ConfigRef *corev1.ObjectReference `json:"configRef,omitempty"`

	// DataSecretName is the name of the secret that stores ssh keys.
	// +optional
	DataSecretName *string `json:"dataSecretName,omitempty"`
}

// ControlPlaneServiceTemplate describes the template for the control plane service.
type ControlPlaneServiceTemplate struct {
	// Service metadata allows to set labels and annotations for the service.
	// This field is optional.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	// Service specification allows to override some fields in the service spec.
	// Note, it does not aim cover all fields of the service spec.
	// +optional
	Spec ServiceSpecTemplate `json:"spec,omitempty"`
}

// ServiceSpecTemplate describes the service spec template.
type ServiceSpecTemplate struct {
	// Type determines how the Service is exposed. Defaults to ClusterIP. Valid
	// options are ExternalName, ClusterIP, NodePort, and LoadBalancer.
	// More info: https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
}

// +kubebuilder:resource:path=kubevirtclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:object:root=true

// KubevirtCluster is the Schema for the kubevirtclusters API.
type KubevirtCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubevirtClusterSpec   `json:"spec,omitempty"`
	Status KubevirtClusterStatus `json:"status,omitempty"`
}

func (c *KubevirtCluster) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

func (c *KubevirtCluster) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// KubevirtClusterList contains a list of KubevirtCluster.
type KubevirtClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubevirtCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubevirtCluster{}, &KubevirtClusterList{})
}
