package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&ProviderPlatform{}, &ProviderPlatformList{})
}

const (
	CloudProviderConfiguredAsExpected    = "CloudProviderInfrastructureConfiguredAsExpected"
	CloudProviderMisConfiguredReason     = "CloudProviderInfrastructureMisconfigured"
	CloudProviderIAMConfiguredAsExpected = "CloudProviderIAMConfiguredAsExpected"
	CloudProviderIAMMisConfiguredReason  = "CloudProviderIAMMisconfigured"

	// CloudProviderConfigured indicates (if status is true) that the
	// Cloud provider configuration specified for the hostedCluster has been deployed
	CloudProviderConfigured    ConditionType = "CloudProviderInfrastructureConfigured"
	CloudProviderIAMConfigured ConditionType = "CloudProviderIAMConfigured"
)

// ProviderPlatformSpec defines the desired state of HostedCluster
type ProviderPlatformSpec struct {

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

	// InfraID is used to identify the cluster in cloud platforms
	// +optional
	InfraID string `json:"infraID,omitempty"`

	// ProviderPlatformCreds is a reference to the AWS credentials needed to provision the
	// Infrastructure and IAM
	// +immutable
	ProviderPlatformCreds corev1.LocalObjectReference `json:"providerPlatformCreds"`
}

type ProviderPlatformStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient

// ProviderPlatform is the primary representation of a HyperShift cluster and encapsulates
// the control plane and common data plane configuration. Creating a ProviderPlatform
// results in a fully functional OpenShift control plane with no attached nodes.
// To support workloads (e.g. pods), a ProviderPlatform may have one or more associated
// NodePool resources.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=providerplatforms,shortName=pp;pps,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version.history[?(@.state==\"Completed\")].version",description="Version"
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// +kubebuilder:printcolumn:name="Progress",type="string",JSONPath=".status.version.history[?(@.state!=\"\")].state",description="Progress"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].reason",description="Reason"
type ProviderPlatform struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the HostedCluster.
	Spec ProviderPlatformSpec `json:"spec"`

	// Status is the latest observed status of the HostedCluster.
	Status ProviderPlatformStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// HostedClusterList contains a list of HostedCluster
type ProviderPlatformList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderPlatform `json:"items"`
}
