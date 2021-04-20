package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&MachineConfigServer{}, &MachineConfigServerList{})
}

// MachineConfigServerSpec defines the desired state of MachineConfigServer
type MachineConfigServerSpec struct {

	// Release specifies the release image to use for this MachineConfigServer
	ReleaseImage string `json:"releaseImage"`

	// IgnitionService defines how the MachineConfigServer service is published in the management cluster
	IgnitionService ServicePublishingStrategy `json:"ignitionService"`
}

// MachineConfigServerStatus defines the observed state of MachineConfigServer
type MachineConfigServerStatus struct {
	// Version is the semantic version of the release used by the mcs.
	// For a mcs a given version represents the ignition config served by
	// the ignition endpoint referenced in the userdata secret.
	// +kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`

	// +kubebuilder:validation:Optional
	Host string `json:"host,omitempty"`

	// +kubebuilder:validation:Optional
	Port int32 `json:"port,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=machineconfigservers,shortName=mcs;mcss,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version",description="Version"
// +kubebuilder:printcolumn:name="Host",type="string",JSONPath=".status.host",description="Host"
// MachineConfigServer is the Schema for the MachineConfigServers API
type MachineConfigServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MachineConfigServerSpec   `json:"spec,omitempty"`
	Status MachineConfigServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// MachineConfigServerList contains a list of MachineConfigServer
type MachineConfigServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MachineConfigServer `json:"items"`
}
