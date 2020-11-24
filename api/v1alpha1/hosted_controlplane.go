package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&HostedControlPlane{})
	SchemeBuilder.Register(&HostedControlPlaneList{})
}

// HostedControlPlane defines the desired state of HostedControlPlane
// +kubebuilder:resource:path=hostedcontrolplanes,shortName=hcp;hcps,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true
type HostedControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostedControlPlaneSpec   `json:"spec,omitempty"`
	Status HostedControlPlaneStatus `json:"status,omitempty"`
}

// HostedControlPlaneSpec defines the desired state of HostedControlPlane
type HostedControlPlaneSpec struct {
	BaseDomain   string `json:"baseDomain"`
	PullSecret   string `json:"pullSecret"`
	ServiceCIDR  string `json:"serviceCIDR"`
	PodCIDR      string `json:"podCIDR"`
	SSHKey       string `json:"sshKey"`
	ReleaseImage string `json:"releaseImage"`
}

// HostedControlPlaneStatus defines the observed state of HostedControlPlane
type HostedControlPlaneStatus struct {
	// Ready denotes that the HostedControlPlane API Server is ready to
	// receive requests
	// +kubebuilder:default=false
	Ready                       bool        `json:"ready"`
	ExternalManagedControlPlane bool        `json:"externalManagedControlPlane"`
	ControlPlaneEndpoint        APIEndpoint `json:"controlPlaneEndpoint"`
}

// +kubebuilder:object:root=true
// HostedControlPlaneList contains a list of HostedControlPlanes.
type HostedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlane `json:"items"`
}
