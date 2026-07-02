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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
)

type ClusterDeploymentReference struct {
	// Name is unique within a namespace to reference a clusterDeployment resource.
	// +optional
	Name string `json:"name,omitempty"`
	// Namespace defines the space within which the clusterDeployment name must be unique.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// AgentClusterSpec defines the desired state of AgentCluster
type AgentClusterSpec struct {
	// IgnitionEndpoint store the data to of the custom ignition endpoint.
	// +optional
	IgnitionEndpoint *IgnitionEndpoint `json:"ignitionEndpoint,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`
}

// IgnitionEndpoint store the data to of the custom ignition endpoint.
type IgnitionEndpoint struct {
	// Url store the URL of the custom ignition endpoint.
	// +optional
	Url string `json:"url,omitempty"`

	// CaCertificateReference is a reference to the secret containing CA certificate to be used
	// when contacting the URL via HTTPS.
	// +optional
	CaCertificateReference *CaCertificateReference `json:"caCertificateReference,omitempty"`
}

type CaCertificateReference struct {
	// Namespace is the namespace of the secret containing the CA certificate.
	Namespace string `json:"namespace"`
	// Name is the name of the secret containing the CA certificate.
	Name string `json:"name"`
}

// AgentClusterStatus defines the observed state of AgentCluster
type AgentClusterStatus struct {
	// +optional
	Ready bool `json:"ready"`

	// ClusterDeploymentRef is a reference to the associated Hive ClusterDeployment.
	ClusterDeploymentRef ClusterDeploymentReference `json:"clusterDeploymentReference"`

	// Conditions defines current service state of the ClusterDeployment.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// FailureDomains is a list of failure domain objects synced from the infrastructure provider.
	FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:deprecatedversion:warning="v1alpha1 is a deprecated version for AgentCluster"

// AgentCluster is the Schema for the agentclusters API
type AgentCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentClusterSpec   `json:"spec,omitempty"`
	Status AgentClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AgentClusterList contains a list of AgentCluster
type AgentClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentCluster{}, &AgentClusterList{})
}
