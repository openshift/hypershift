package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&GuestCluster{})
	SchemeBuilder.Register(&GuestClusterList{})
}

// +kubebuilder:resource:path=guestclusters,shortName=gc;gcs,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// GuestCluster is the Schema for the GuestCluster API
type GuestCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GuestClusterSpec   `json:"spec,omitempty"`
	Status GuestClusterStatus `json:"status,omitempty"`
}

// GuestClusterSpec defines the desired state of GuestCluster
type GuestClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// +optional
	ComputeReplicas int `json:"computeReplicas,omitempty"`

	Region string `json:"region,omitempty"`

	// TODO (alberto): populate the API and create/consume infrastructure via aws sdk
	// role profile, sg, vpc, subnets.
}

type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int32 `json:"port"`
}

// GuestClusterStatus defines the observed state of GuestCluster
type GuestClusterStatus struct {
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// GuestClusterList contains a list of GuestClusters.
type GuestClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GuestCluster `json:"items"`
}
