package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

func init() {
	SchemeBuilder.Register(&HostedCluster{}, &HostedClusterList{})
}

const (
	// AuditWebhookKubeconfigKey is the key name in the AuditWebhook secret that stores audit webhook kubeconfig
	AuditWebhookKubeconfigKey = "webhook-kubeconfig"
)

// HostedClusterSpec defines the desired state of HostedCluster
type HostedClusterSpec struct {

	// Release specifies the release image to use for this HostedCluster
	Release Release `json:"release"`

	// PullSecret is a pull secret injected into the container runtime of guest
	// workers. It should have an ".dockerconfigjson" key containing the pull secret JSON.
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// AuditWebhook contains metadata for configuring an audit webhook
	// endpoint for a cluster to process cluster audit events. It references
	// a secret that contains the webhook information for the audit webhook endpoint.
	// It is a secret because if the endpoint has MTLS the kubeconfig will contain client
	// keys. This is currently only supported in IBM Cloud. The kubeconfig needs to be stored
	// in the secret with a secret key name that corresponds to the constant AuditWebhookKubeconfigKey.
	// +optional
	AuditWebhook *corev1.LocalObjectReference `json:"auditWebhook,omitempty"`

	// SigningKey is a reference to a Secret containing a single key "key"
	// +optional
	SigningKey corev1.LocalObjectReference `json:"signingKey,omitempty"`

	// +kubebuilder:default:="https://kubernetes.default.svc"
	IssuerURL string `json:"issuerURL"`

	// SSHKey is a reference to a Secret containing a single key "id_rsa.pub",
	// whose value is the public part of an SSH key that can be used to access
	// Nodes.
	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// Networking contains network-specific settings for this cluster
	Networking ClusterNetworking `json:"networking"`

	// Autoscaling for compute nodes only, does not cover control plane
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	Platform PlatformSpec `json:"platform"`

	// InfraID is used to identify the cluster in cloud platforms
	InfraID string `json:"infraID,omitempty"`

	// DNS configuration for the cluster
	DNS DNSSpec `json:"dns,omitempty"`

	// Services defines metadata about how control plane services are published
	// in the management cluster.
	// TODO (alberto): include Ignition endpoint here.
	Services []ServicePublishingStrategyMapping `json:"services"`

	// ControllerAvailabilityPolicy specifies whether to run control plane controllers in HA mode
	// Defaults to SingleReplica when not set.
	// +optional
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// Etcd contains metadata about the etcd cluster the hypershift managed Openshift control plane components
	// use to store data. Changing the ManagementType for the etcd cluster is not supported after initial creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={managementType: "Managed"   }
	Etcd EtcdSpec `json:"etcd"`
}

// ServicePublishingStrategyMapping defines the service being published and  metadata about the publishing strategy.
type ServicePublishingStrategyMapping struct {
	// Service identifies the type of service being published
	// +kubebuilder:validation:Enum=APIServer;VPN;OAuthServer;OIDC;Konnectivity
	Service                   ServiceType `json:"service"`
	ServicePublishingStrategy `json:"servicePublishingStrategy"`
}

// ServicePublishingStrategy defines metadata around how a service is published
type ServicePublishingStrategy struct {
	// Type defines the publishing strategy used for the service.
	// +kubebuilder:validation:Enum=LoadBalancer;NodePort;Route;None
	Type PublishingStrategyType `json:"type"`
	// NodePort is used to define extra metadata for the NodePort publishing strategy.
	NodePort *NodePortPublishingStrategy `json:"nodePort,omitempty"`
}

// PublishingStrategyType defines publishing strategies for services.
type PublishingStrategyType string

var (
	// LoadBalancer exposes  a service with a LoadBalancer kube service.
	LoadBalancer PublishingStrategyType = "LoadBalancer"
	// NodePort exposes a service with a NodePort kube service.
	NodePort PublishingStrategyType = "NodePort"
	// Route exposes services with a Route + ClusterIP kube service.
	Route PublishingStrategyType = "Route"
	// None disables exposing the service
	None PublishingStrategyType = "None"
)

// ServiceType defines what control plane services can be exposed from the management control plane
type ServiceType string

var (
	APIServer    ServiceType = "APIServer"
	VPN          ServiceType = "VPN"
	Konnectivity ServiceType = "Konnectivity"
	OAuthServer  ServiceType = "OAuthServer"
	OIDC         ServiceType = "OIDC"
)

// NodePortPublishingStrategy defines the network endpoint that can be used to contact the NodePort service
type NodePortPublishingStrategy struct {
	// Address is the host/ip that the nodePort service is exposed over
	Address string `json:"address"`
	// Port is the nodePort of the service. If <=0 the nodePort is dynamically assigned when the service is created
	Port int32 `json:"port,omitempty"`
}

// DNSSpec specifies the DNS configuration in the cluster
type DNSSpec struct {
	// BaseDomain is the base domain of the cluster.
	BaseDomain string `json:"baseDomain"`

	// PublicZoneID is the Hosted Zone ID where all the DNS records that are publicly accessible to
	// the internet exist.
	// +optional
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// PrivateZoneID is the Hosted Zone ID where all the DNS records that are only available internally
	// to the cluster exist.
	// +optional
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}

type ClusterNetworking struct {
	ServiceCIDR string `json:"serviceCIDR"`
	PodCIDR     string `json:"podCIDR"`
	MachineCIDR string `json:"machineCIDR"`
}

// PlatformType is a specific supported infrastructure provider.
// +kubebuilder:validation:Enum=AWS;None;IBMCloud
type PlatformType string

const (
	// AWSPlatformType represents Amazon Web Services infrastructure.
	AWSPlatform PlatformType = "AWS"

	NonePlatform PlatformType = "None"

	IBMCloudPlatform PlatformType = "IBMCloud"
)

const (
	SecurePortOverrideAnnotation  = "hypershift.openshift.io/secureport-override"
	IdentityProviderAnnotation    = "hypershift.openshift.io/identity-provider"
	NamedCertAnnotation           = "hypershift.openshift.io/named-cert"
	NetworkTypeOverrideAnnotation = "hypershift.openshift.io/networktype-override"
	NamedCertSecretName           = "named-cert-info"
	PortierisImageAnnotation      = "hypershift.openshift.io/portieris-image"
	KMSImageAnnotation            = "hypershift.openshift.io/kms-image"
	KMSKPInfoAnnotation           = "hypershift.openshift.io/kms-kp-info"
	KMSKPRegionAnnotation         = "hypershift.openshift.io/kms-kp-region"
)

type PlatformSpec struct {
	// Type is the underlying infrastructure provider for the cluster.
	//
	// +unionDiscriminator
	Type PlatformType `json:"type"`

	// AWS contains AWS-specific settings for the HostedCluster
	// +optional
	AWS *AWSPlatformSpec `json:"aws,omitempty"`
}

type AWSCloudProviderConfig struct {
	// Subnet is the subnet to use for instances
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

	// Zone is the availability zone where the instances are created
	// +optional
	Zone string `json:"zone,omitempty"`

	// VPC specifies the VPC used for the cluster
	VPC string `json:"vpc"`
}

type AWSPlatformSpec struct {
	// Region is the AWS region for the cluster.
	// This is used by CRs that are consumed by OCP Operators.
	// E.g cluster-infrastructure-02-config.yaml and install-config.yaml
	// This is also used by nodePools to fetch the default boot AMI in a given payload.
	Region string `json:"region"`

	// CloudProviderConfig is used to generate the ConfigMap with the cloud config consumed
	// by the Control Plane components.
	// +optional
	CloudProviderConfig *AWSCloudProviderConfig `json:"cloudProviderConfig,omitempty"`

	// ServiceEndpoints list contains custom endpoints which will override default
	// service endpoint of AWS Services.
	// There must be only one ServiceEndpoint for a service.
	// +optional
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`

	Roles []AWSRoleCredentials `json:"roles,omitempty"`

	// KubeCloudControllerCreds is a reference to a secret containing cloud
	// credentials with permissions matching the Kube cloud controller policy.
	// The secret should have exactly one key, `credentials`, whose value is
	// an AWS credentials file.
	KubeCloudControllerCreds corev1.LocalObjectReference `json:"kubeCloudControllerCreds"`

	// NodePoolManagementCreds is a reference to a secret containing cloud
	// credentials with permissions matching the noe pool management policy.
	// The secret should have exactly one key, `credentials`, whose value is
	// an AWS credentials file.
	NodePoolManagementCreds corev1.LocalObjectReference `json:"nodePoolManagementCreds"`
}

type AWSRoleCredentials struct {
	ARN       string `json:"arn"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
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
	// +kubebuilder:validation:Pattern=^(\w+\S+)$
	Image string `json:"image"`
}

// TODO maybe we have profiles for scaling behaviors
type ClusterAutoscaling struct {
	// Maximum number of nodes in all node groups.
	// Cluster autoscaler will not grow the cluster beyond this number.
	// +kubebuilder:validation:Minimum=0
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"`

	// Gives pods graceful termination time before scaling down
	// default: 600 seconds
	// +kubebuilder:validation:Minimum=0
	MaxPodGracePeriod *int32 `json:"maxPodGracePeriod,omitempty"`

	// Maximum time CA waits for node to be provisioned
	// default: 15 minutes
	// +kubebuilder:validation:Pattern=^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$
	MaxNodeProvisionTime string `json:"maxNodeProvisionTime,omitempty"`

	// To allow users to schedule "best-effort" pods, which shouldn't trigger
	// Cluster Autoscaler actions, but only run when there are spare resources available,
	// default: -10
	// More info: https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption
	PodPriorityThreshold *int32 `json:"podPriorityThreshold,omitempty"`
}

// EtcdManagementType is a enum specifying the strategy for managing the cluster's etcd instance
// +kubebuilder:validation:Enum=Managed;Unmanaged
type EtcdManagementType string

const (
	Managed   EtcdManagementType = "Managed"
	Unmanaged EtcdManagementType = "Unmanaged"
)

type EtcdSpec struct {
	// ManagementType defines how the etcd cluster is managed. Unmanaged means
	// the etcd cluster is managed by a system outside the hypershift controllers.
	// Managed means the hypershift controllers manage the provisioning of the etcd cluster
	// and the operations around it
	// +unionDiscriminator
	ManagementType EtcdManagementType `json:"managementType"`

	// Managed provides metadata that defines how the hypershift controllers manage the etcd cluster
	// +optional
	Managed *ManagedEtcdSpec `json:"managed,omitempty"`

	// Unmanaged provides metadata that enables the Openshift controllers to connect to the external etcd cluster
	// +optional
	Unmanaged *UnmanagedEtcdSpec `json:"unmanaged,omitempty"`
}

type ManagedEtcdSpec struct {

	//TODO: Ultimately backup policies, etc can be defined here.
}

// UnmanagedEtcdSpec defines metadata that enables the Openshift controllers to connect to the external etcd cluster
type UnmanagedEtcdSpec struct {
	// Endpoint is the full url to connect to the etcd cluster endpoint. An example is
	// https://etcd-client:2379
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://`
	Endpoint string `json:"endpoint"`

	// TLS defines a reference to a TLS secret that can be used for client MTLS authentication with
	// the etcd cluster
	TLS EtcdTLSConfig `json:"tls"`
}

type EtcdTLSConfig struct {
	// Client refers to a secret for client MTLS authentication with the etcd cluster
	// The CA must be stored at secret key etcd-client-ca.crt.
	// The client cert must be stored at secret key etcd-client.crt.
	// The client key must be stored at secret key etcd-client.key.
	ClientSecret corev1.LocalObjectReference `json:"clientSecret"`
}

const (
	// HostedClusterAvailable indicates whether the HostedCluster has a healthy
	// control plane.
	HostedClusterAvailable ConditionType = "Available"

	// IgnitionEndpointAvailable indicates whether the ignition server for the
	// HostedCluster is available to handle ignition requests.
	IgnitionEndpointAvailable ConditionType = "IgnitionEndpointAvailable"
)

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	IgnitionServerDeploymentAsExpected          = "IgnitionServerDeploymentAsExpected"
	IgnitionServerDeploymentStatusUnknownReason = "IgnitionServerDeploymentStatusUnknown"
	IgnitionServerDeploymentNotFoundReason      = "IgnitionServerDeploymentNotFound"
	IgnitionServerDeploymentUnavailableReason   = "IgnitionServerDeploymentUnavailable"
)

// The following are reasons for the HostedClusterAvailable condition.
const (
	HostedClusterIsAvailable          = "HostedClusterIsAvailable"
	HostedClusterInsufficientMetadata = "HostedClusterInsufficientMetadata"
)

// HostedClusterStatus defines the observed state of HostedCluster
type HostedClusterStatus struct {
	// Version is the status of the release version applied to the
	// HostedCluster.
	// +optional
	Version *ClusterVersionStatus `json:"version,omitempty"`

	// KubeConfig is a reference to the secret containing the default kubeconfig
	// for the cluster.
	// +optional
	KubeConfig *corev1.LocalObjectReference `json:"kubeconfig,omitempty"`

	// IgnitionEndpoint is the endpoint injected in the ign config userdata.
	// It exposes the config for instances to become kubernetes nodes.
	// +optional
	IgnitionEndpoint string `json:"ignitionEndpoint"`

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
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// +kubebuilder:printcolumn:name="Progress",type="string",JSONPath=".status.version.history[?(@.state!=\"\")].state",description="Progress"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].reason",description="Reason"
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
