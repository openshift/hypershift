package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

func init() {
	SchemeBuilder.Register(&HostedCluster{}, &HostedClusterList{})
}

// HostedClusterSpec defines the desired state of HostedCluster
type HostedClusterSpec struct {

	// Release specifies the release image to use for this HostedCluster
	Release Release `json:"release"`

	InitialComputeReplicas int `json:"initialComputeReplicas"`

	// PullSecret is a pull secret injected into the container runtime of guest
	// workers. It should have an ".dockerconfigjson" key containing the pull secret JSON.
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// ProviderCreds is a reference to a secret containing cloud account info
	ProviderCreds corev1.LocalObjectReference `json:"providerCreds"`

	// Networking contains network-specific settings for this cluster
	Networking ClusterNetworking `json:"networking"`

	Platform PlatformSpec `json:"platform"`

	// InfraID is used to identify the cluster in cloud platforms
	InfraID string `json:"infraID,omitempty"`

	ControlPlaneServiceType                string `json:"controlPlaneServiceType,omitempty"`
	ControlPlaneServiceTypeNodePortAddress string `json:"controlPlaneServiceTypeNodePortAddress,omitempty"`
}

type ClusterNetworking struct {
	ServiceCIDR string `json:"serviceCIDR"`
	PodCIDR     string `json:"podCIDR"`
	MachineCIDR string `json:"machineCIDR"`
}

type PlatformSpec struct {
	// AWS contains AWS-specific settings for the HostedCluster
	// +optional
	AWS *AWSPlatformSpec `json:"aws,omitempty"`
}

type AWSPlatformSpec struct {
	// Region is the AWS region for the cluster
	Region string `json:"region"`

	// VPC specifies the VPC used for the cluster
	VPC string `json:"vpc"`

	// NodePoolDefaults specifies the default platform
	// +optional
	NodePoolDefaults *AWSNodePoolPlatform `json:"nodePoolDefaults,omitempty"`

	// ServiceEndpoints list contains custom endpoints which will override default
	// service endpoint of AWS Services.
	// There must be only one ServiceEndpoint for a service.
	// +optional
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`
}

// AWSServiceEndpoint stores the configuration for services to
// override existing defaults of AWS Services.
type AWSServiceEndpoint struct {
	// Name is the name of the AWS service.
	// This must be provided and cannot be empty.
	Name string `json:"name"`

	// URL is fully qualified URI with scheme https, that overrides the default generated
	// endpoint for a client.
	// This must be provided and cannot be empty.
	//
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
}

type Release struct {
	// Image is the release image pullspec for the control plane
	// +kubebuilder:validation:Required
	Image string `json:"image"`
}

// HostedClusterStatus defines the observed state of HostedCluster
type HostedClusterStatus struct {
	// Version is the status of the release version applied to the
	// HostedCluster.
	// +optional
	Version *ClusterVersionStatus `json:"version,omitempty"`

	// +optional
	Ready bool `json:"ready,omitempty"`

	// KubeConfig is a reference to the secret containing the default kubeconfig
	// for the cluster.
	// +optional
	KubeConfig *corev1.LocalObjectReference `json:"kubeconfig,omitempty"`

	Conditions []metav1.Condition `json:"conditions"`
}

// ClusterVersionStatus reports the status of the cluster versioning,
// including any upgrades that are in progress. The current field will
// be set to whichever version the cluster is reconciling to, and the
// conditions array will report whether the update succeeded, is in
// progress, or is failing.
// +k8s:deepcopy-gen=true
type ClusterVersionStatus struct {
	// desired is the version that the cluster is reconciling towards.
	// If the cluster is not yet fully initialized desired will be set
	// with the information available, which may be an image or a tag.
	// +kubebuilder:validation:Required
	// +required
	Desired Release `json:"desired"`

	// history contains a list of the most recent versions applied to the cluster.
	// This value may be empty during cluster startup, and then will be updated
	// when a new update is being applied. The newest update is first in the
	// list and it is ordered by recency. Updates in the history have state
	// Completed if the rollout completed - if an update was failing or halfway
	// applied the state will be Partial. Only a limited amount of update history
	// is preserved.
	// +optional
	History []configv1.UpdateHistory `json:"history,omitempty"`

	// observedGeneration reports which version of the spec is being synced.
	// If this value is not equal to metadata.generation, then the desired
	// and conditions fields may represent a previous version.
	// +kubebuilder:validation:Required
	// +required
	ObservedGeneration int64 `json:"observedGeneration"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hostedclusters,shortName=hc;hcs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version.history[?(@.state==\"Completed\")].version",description="Version"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Ready"
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// HostedCluster is the Schema for the hostedclusters API
type HostedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostedClusterSpec   `json:"spec,omitempty"`
	Status HostedClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// HostedClusterList contains a list of HostedCluster
type HostedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedCluster `json:"items"`
}
