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
	IssuerURL    string                      `json:"issuerURL"`

	// Networking specifies network configuration for the cluster.
	// Temporarily optional for backward compatibility, required in future releases.
	// +optional
	Networking ClusterNetworking `json:"networking,omitempty"`

	// deprecated
	// use networking.ServiceNetwork
	// +optional
	ServiceCIDR string `json:"serviceCIDR,omitempty"`

	// deprecated
	// use networking.ClusterNetwork
	// +optional
	PodCIDR string `json:"podCIDR,omitempty"`

	// deprecated
	// use networking.MachineNetwork
	// +optional
	MachineCIDR string `json:"machineCIDR,omitempty"`

	// deprecated
	// use networking.NetworkType
	// NetworkType specifies the SDN provider used for cluster networking.
	// +optional
	NetworkType NetworkType `json:"networkType,omitempty"`

	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// ClusterID is the unique id that identifies the cluster externally.
	// Making it optional here allows us to keep compatibility with previous
	// versions of the control-plane-operator that have no knowledge of this
	// field.
	// +optional
	ClusterID string `json:"clusterID,omitempty"`

	InfraID  string       `json:"infraID"`
	Platform PlatformSpec `json:"platform"`
	DNS      DNSSpec      `json:"dns"`

	// ServiceAccountSigningKey is a reference to a secret containing the private key
	// used by the service account token issuer. The secret is expected to contain
	// a single key named "key". If not specified, a service account signing key will
	// be generated automatically for the cluster.
	//
	// +optional
	ServiceAccountSigningKey *corev1.LocalObjectReference `json:"serviceAccountSigningKey,omitempty"`

	// deprecated
	// use networking.apiServer.APIPort
	// APIPort is the port at which the APIServer listens inside a worker
	// +optional
	APIPort *int32 `json:"apiPort,omitempty"`

	// deprecated
	// use networking.apiServer.AdvertiseAddress
	// APIAdvertiseAddress is the address at which the APIServer listens
	// inside a worker.
	// +optional
	APIAdvertiseAddress *string `json:"apiAdvertiseAddress,omitempty"`

	// deprecated
	// use networking.apiServer.APIAllowedCIDRBlocks
	// APIAllowedCIDRBlocks is an allow list of CIDR blocks that can access the APIServer
	// If not specified, traffic is allowed from all addresses.
	// This depends on underlying support by the cloud provider for Service LoadBalancerSourceRanges
	// +optional
	APIAllowedCIDRBlocks []CIDRBlock `json:"apiAllowedCIDRBlocks,omitempty"`

	// ControllerAvailabilityPolicy specifies the availability policy applied to
	// critical control plane components. The default value is SingleReplica.
	//
	// +optional
	// +kubebuilder:default:="SingleReplica"
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// InfrastructureAvailabilityPolicy specifies the availability policy applied
	// to infrastructure services which run on cluster nodes. The default value is
	// SingleReplica.
	//
	// +optional
	// +kubebuilder:default:="SingleReplica"
	InfrastructureAvailabilityPolicy AvailabilityPolicy `json:"infrastructureAvailabilityPolicy,omitempty"`

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

	// AdditionalTrustBundle references a ConfigMap containing a PEM-encoded X.509 certificate bundle
	// +optional
	AdditionalTrustBundle *corev1.LocalObjectReference `json:"additionalTrustBundle,omitempty"`

	// SecretEncryption contains metadata about the kubernetes secret encryption strategy being used for the
	// cluster when applicable.
	// +optional
	SecretEncryption *SecretEncryptionSpec `json:"secretEncryption,omitempty"`

	// PausedUntil is a field that can be used to pause reconciliation on a resource.
	// Either a date can be provided in RFC3339 format or a boolean. If a date is
	// provided: reconciliation is paused on the resource until that date. If the boolean true is
	// provided: reconciliation is paused on the resource until the field is removed.
	// +optional
	PausedUntil *string `json:"pausedUntil,omitempty"`

	// OLMCatalogPlacement specifies the placement of OLM catalog components. By default,
	// this is set to management and OLM catalog components are deployed onto the management
	// cluster. If set to guest, the OLM catalog components will be deployed onto the guest
	// cluster.
	//
	// +kubebuilder:default=management
	// +optional
	// +immutable
	OLMCatalogPlacement OLMCatalogPlacement `json:"olmCatalogPlacement,omitempty"`

	// Autoscaling specifies auto-scaling behavior that applies to all NodePools
	// associated with the control plane.
	//
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	// NodeSelector when specified, must be true for the pods managed by the HostedCluster to be scheduled.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// AvailabilityPolicy specifies a high level availability policy for components.
type AvailabilityPolicy string

const (
	// HighlyAvailable means components should be resilient to problems across
	// fault boundaries as defined by the component to which the policy is
	// attached. This usually means running critical workloads with 3 replicas and
	// with little or no toleration of disruption of the component.
	HighlyAvailable AvailabilityPolicy = "HighlyAvailable"

	// SingleReplica means components are not expected to be resilient to problems
	// across most fault boundaries associated with high availability. This
	// usually means running critical workloads with just 1 replica and with
	// toleration of full disruption of the component.
	SingleReplica AvailabilityPolicy = "SingleReplica"
)

type KubeconfigSecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type ConditionType string

const (
	HostedControlPlaneAvailable          ConditionType = "Available"
	HostedControlPlaneDegraded           ConditionType = "Degraded"
	EtcdAvailable                        ConditionType = "EtcdAvailable"
	EtcdSnapshotRestored                 ConditionType = "EtcdSnapshotRestored"
	KubeAPIServerAvailable               ConditionType = "KubeAPIServerAvailable"
	InfrastructureReady                  ConditionType = "InfrastructureReady"
	ValidHostedControlPlaneConfiguration ConditionType = "ValidHostedControlPlaneConfiguration"
	ClusterVersionFailing                ConditionType = "ClusterVersionFailing"
	CVOScaledDown                        ConditionType = "CVOScaledDown"
	CloudResourcesDestroyed              ConditionType = "CloudResourcesDestroyed"
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

	// OAuthCallbackURLTemplate contains a template for the URL to use as a callback
	// for identity providers. The [identity-provider-name] placeholder must be replaced
	// with the name of an identity provider defined on the HostedCluster.
	// This is populated after the infrastructure is ready.
	// +kubebuilder:validation:Optional
	OAuthCallbackURLTemplate string `json:"oauthCallbackURLTemplate,omitempty"`

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

	// KubeadminPassword is a reference to the secret containing the initial kubeadmin password
	// for the guest cluster.
	// +optional
	KubeadminPassword *corev1.LocalObjectReference `json:"kubeadminPassword,omitempty"`

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
