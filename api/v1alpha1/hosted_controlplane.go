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
	// NetworkType specifies the SDN provider used for cluster networking.
	NetworkType NetworkType                 `json:"networkType"`
	SSHKey      corev1.LocalObjectReference `json:"sshKey"`
	InfraID     string                      `json:"infraID"`
	Platform    PlatformSpec                `json:"platform"`
	DNS         DNSSpec                     `json:"dns"`

	// APIPort is the port at which the APIServer listens inside a worker
	// +optional
	APIPort *int32 `json:"apiPort,omitempty"`
	// APIAdvertiseAddress is the address at which the APIServer listens
	// inside a worker.
	// +optional
	APIAdvertiseAddress *string `json:"apiAdvertiseAddress,omitempty"`

	// ControllerAvailabilityPolicy specifies whether to run control plane controllers in HA mode
	// Defaults to SingleReplica when not set
	// +optional
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// FIPS specifies if the nodes for the cluster will be running in FIPS mode
	// +optional
	FIPS bool `json:"fips"`

	// KubeConfig specifies the name and key for the kubeconfig secret
	// +optional
	KubeConfig *KubeconfigSecretRef `json:"kubeconfig,omitempty"`

	// Services defines metadata about how control plane services are published
	// in the management cluster.
	Services []ServicePublishingStrategyMapping `json:"services"`

	// AuditWebhook contains metadata for configuring an audit webhook
	// endpoint for a cluster to process cluster audit events. It references
	// a secret that contains the webhook information for the audit webhook endpoint.
	// It is a secret because if the endpoint has MTLS the kubeconfig will contain client
	// keys. This is currently only supported in IBM Cloud. The kubeconfig needs to be stored
	// in the secret with a secret key name that corresponds to the constant AuditWebhookKubeconfigKey.
	// +optional
	AuditWebhook *corev1.LocalObjectReference `json:"auditWebhook,omitempty"`

	// Etcd contains metadata about the etcd cluster the hypershift managed Openshift control plane components
	// use to store data.
	Etcd EtcdSpec `json:"etcd"`

	// Configuration embeds resources that correspond to the openshift configuration API:
	// https://docs.openshift.com/container-platform/4.7/rest_api/config_apis/config-apis-index.html
	// +kubebuilder:validation:Optional
	Configuration *ClusterConfiguration `json:"configuration,omitempty"`

	// ImageContentSources lists sources/repositories for the release-image content.
	// +optional
	ImageContentSources []ImageContentSource `json:"imageContentSources,omitempty"`
}

type AvailabilityPolicy string

const (
	HighlyAvailable AvailabilityPolicy = "HighlyAvailable"
	SingleReplica   AvailabilityPolicy = "SingleReplica"
)

type KubeconfigSecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type ConditionType string

const (
	HostedControlPlaneAvailable ConditionType = "Available"
	EtcdAvailable               ConditionType = "EtcdAvailable"
	KubeAPIServerAvailable      ConditionType = "KubeAPIServerAvailable"
	InfrastructureReady         ConditionType = "InfrastructureReady"
	ValidConfiguration          ConditionType = "ValidConfiguration"
	ClusterVersionFailing       ConditionType = "ClusterVersionFailing"
)

// HostedControlPlaneStatus defines the observed state of HostedControlPlane
type HostedControlPlaneStatus struct {
	// Ready denotes that the HostedControlPlane API Server is ready to
	// receive requests
	// This satisfies CAPI contract https://github.com/kubernetes-sigs/cluster-api/blob/cd3a694deac89d5ebeb888307deaa61487207aa0/controllers/cluster_controller_phases.go#L226-L230
	// +kubebuilder:validation:Required
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Initialized denotes whether or not the control plane has
	// provided a kubeadm-config.
	// Once this condition is marked true, its value is never changed. See the Ready condition for an indication of
	// the current readiness of the cluster's control plane.
	// This satisfies CAPI contract https://github.com/kubernetes-sigs/cluster-api/blob/cd3a694deac89d5ebeb888307deaa61487207aa0/controllers/cluster_controller_phases.go#L238-L252
	// +kubebuilder:validation:Required
	// +kubebuilder:default=false
	Initialized bool `json:"initialized"`

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
	Conditions []metav1.Condition `json:"conditions"`
}

type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int32 `json:"port"`
}

// +kubebuilder:object:root=true
// HostedControlPlaneList contains a list of HostedControlPlanes.
type HostedControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedControlPlane `json:"items"`
}
