package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&PlatformConfiguration{}, &PlatformConfigurationList{})
}

const (
	PlatformConfiguredAsExpected    = "PlatformInfrastructureConfiguredAsExpected"
	PlatformMisConfiguredReason     = "PlatformInfrastructureMisconfigured"
	PlatformIAMConfiguredAsExpected = "PlatformIAMConfiguredAsExpected"
	PlatformIAMMisConfiguredReason  = "PlatformIAMMisconfigured"

	// PlatformConfigured indicates (if status is true) that the
	// platform configuration specified for the platform provider has been deployed
	PlatformConfigured    ConditionType = "PlatformInfrastructureConfigured"
	PlatformIAMConfigured ConditionType = "PlatformIAMConfigured"
)

// ProviderPlatformSpec defines the desired state of HostedCluster
type PlatformConfigurationSpec struct {

	// PullSecret is a pull secret injected into the container runtime of guest
	// workers. It should have an ".dockerconfigjson" key containing the pull secret JSON.
	// +immutable
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// SSHKey is a reference to a Secret containing a single key "id_rsa.pub",
	// whose value is the public part of an SSH key that can be used to access
	// Nodes.
	// +immutable
	SSHKey corev1.LocalObjectReference `json:"sshKey,omitempty"`

	// Networking contains network-specific settings for this cluster
	// +immutable
	Networking ClusterNetworking `json:"networking,omitempty"`

	// +immutable
	Platform PlatformSpec `json:"platform"`

	// DNS configuration for the cluster
	// +immutable
	DNS DNSSpec `json:"dns"`

	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	IssuerURL string `json:"issuerURL"`

	// SecurityGroups is the set of security groups to associate with nodepool machines
	// +optional
	SecurityGroups []AWSResourceReference `json:"securityGroups,omitempty"`

	// InfraID is used to identify the cluster related Platform Configurations in a provider
	// +optional
	InfraID string `json:"infraID,omitempty"`
}

type PlatformConfigurationStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient

// PlatformConfiguration is the primary representation of a HyperShift cluster and encapsulates
// the control plane and common data plane configuration. Creating a PlatformConfiguration
// results in a fully functional OpenShift control plane with no attached nodes.
// To support workloads (e.g. pods), a PlatformConfiguration may have one or more associated
// NodePool resources.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=platformconfiguration,shortName=pc;pcs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version.history[?(@.state==\"Completed\")].version",description="Version"
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// +kubebuilder:printcolumn:name="Progress",type="string",JSONPath=".status.version.history[?(@.state!=\"\")].state",description="Progress"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].reason",description="Reason"
type PlatformConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the PlatformConfiguration.
	Spec PlatformConfigurationSpec `json:"spec"`

	// Status is the latest observed status of the PlatformConfiguration.
	Status PlatformConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// PlatformConfigurationList contains a list of PlatformConfiguration
type PlatformConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformConfiguration `json:"items"`
}
