package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&ExternalInfraCluster{})
	SchemeBuilder.Register(&ExternalInfraClusterList{})
}

// +kubebuilder:resource:path=externalinfraclusters,shortName=eic;eics,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// ExternalInfraCluster is the Schema for the ExternalInfraCluster API
type ExternalInfraCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalInfraClusterSpec   `json:"spec,omitempty"`
	Status ExternalInfraClusterStatus `json:"status,omitempty"`
}

// ExternalInfraClusterSpec defines the desired state of ExternalInfraCluster
type ExternalInfraClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// +optional
	ComputeReplicas int `json:"computeReplicas,omitempty"`

	// TODO (alberto): populate the API and create/consume infrastructure via aws sdk
	// role profile, sg, vpc, subnets.
	Region string `json:"region"`
}

type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int32 `json:"port"`
}

// ExternalInfraClusterStatus defines the observed state of ExternalInfraCluster
type ExternalInfraClusterStatus struct {
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// ExternalInfraClusterList contains a list of ExternalInfraClusters.
type ExternalInfraClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalInfraCluster `json:"items"`
}
