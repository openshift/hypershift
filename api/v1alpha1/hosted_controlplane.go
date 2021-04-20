package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
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
	ReleaseImage string                      `json:"releaseImage"`
	PullSecret   corev1.LocalObjectReference `json:"pullSecret"`
	SigningKey   corev1.LocalObjectReference `json:"signingKey"`
	IssuerURL    string                      `json:"issuerURL"`
	ServiceCIDR  string                      `json:"serviceCIDR"`
	PodCIDR      string                      `json:"podCIDR"`
	MachineCIDR  string                      `json:"machineCIDR"`
	SSHKey       corev1.LocalObjectReference `json:"sshKey"`
	InfraID      string                      `json:"infraID"`
	Platform     PlatformSpec                `json:"platform"`
	DNS          DNSSpec                     `json:"dns"`

	// KubeConfig specifies the name and key for the kubeconfig secret
	// +optional
	KubeConfig *KubeconfigSecretRef `json:"kubeconfig,omitempty"`

	// Services defines metadata about how control plane services are published
	// in the management cluster.
	Services []ServicePublishingStrategyMapping `json:"services"`
}

type KubeconfigSecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type ConditionType string

const (
	Available     ConditionType = "Available"
	EtcdAvailable ConditionType = "EtcdAvailable"
)

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type HostedControlPlaneCondition struct {
	// type specifies the aspect reported by this condition.
	// +kubebuilder:validation:Required
	Type ConditionType `json:"type"`

	// status of the condition, one of True, False, Unknown.
	// +kubebuilder:validation:Required
	Status ConditionStatus `json:"status"`

	// lastTransitionTime is the time of the last update to the current status property.
	// +kubebuilder:validation:Required
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`

	// reason is the CamelCase reason for the condition's current status.
	// +kubebuilder:validation:Optional
	Reason string `json:"reason,omitempty"`

	// message provides additional information about the current condition.
	// This is only to be consumed by humans.  It may contain Line Feed
	// characters (U+000A), which should be rendered as new lines.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
}

// HostedControlPlaneStatus defines the observed state of HostedControlPlane
type HostedControlPlaneStatus struct {
	// Ready denotes that the HostedControlPlane API Server is ready to
	// receive requests
	// +kubebuilder:validation:Required
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// ExternalManagedControlPlane indicates to cluster-api that the control plane
	// is managed by an external service.
	// https://github.com/kubernetes-sigs/cluster-api/blob/65e5385bffd71bf4aad3cf34a537f11b217c7fab/controllers/machine_controller.go#L468
	// +kubebuilder:default=true
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`

	// ControlPlaneEndpoint contains the endpoint information by which
	// external clients can access the control plane.  This is populated
	// after the infrastructure is ready.
	// +kubebuilder:validation:Optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// Version is the semantic version of the release applied by
	// the hosted control plane operator
	// +kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`

	// ReleaseImage is the release image applied to the hosted control plane.
	ReleaseImage string `json:"releaseImage,omitempty"`

	// lastReleaseImageTransitionTime is the time of the last update to the current
	// releaseImage property.
	// +kubebuilder:validation:Optional
	LastReleaseImageTransitionTime *metav1.Time `json:"lastReleaseImageTransitionTime,omitempty"`

	// KubeConfig is a reference to the secret containing the default kubeconfig
	// for this control plane.
	KubeConfig *KubeconfigSecretRef `json:"kubeConfig,omitempty"`

	// Condition contains details for one aspect of the current state of the HostedControlPlane.
	// Current condition types are: "Available"
	// +kubebuilder:validation:Required
	Conditions []HostedControlPlaneCondition `json:"conditions"`
}

// +kubebuilder:object:root=true
// HostedControlPlaneList contains a list of HostedControlPlanes.
type HostedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlane `json:"items"`
}
