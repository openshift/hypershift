package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"
)

func init() {
	SchemeBuilder.Register(&HostedCluster{}, &HostedClusterList{})
}

const (
	// AuditWebhookKubeconfigKey is the key name in the AuditWebhook secret that stores audit webhook kubeconfig
	AuditWebhookKubeconfigKey                 = "webhook-kubeconfig"
	DisablePKIReconciliationAnnotation        = "hypershift.openshift.io/disable-pki-reconciliation"
	IdentityProviderOverridesAnnotationPrefix = "idpoverrides.hypershift.openshift.io/"
	OauthLoginURLOverrideAnnotation           = "oauth.hypershift.openshift.io/login-url-override"
	// KonnectivityServerImageAnnotation is a temporary annotation that allows the specification of the konnectivity server image.
	// This will be removed when Konnectivity is added to the Openshift release payload
	KonnectivityServerImageAnnotation = "hypershift.openshift.io/konnectivity-server-image"
	// KonnectivityAgentImageAnnotation is a temporary annotation that allows the specification of the konnectivity agent image.
	// This will be removed when Konnectivity is added to the Openshift release payload
	KonnectivityAgentImageAnnotation = "hypershift.openshift.io/konnectivity-agent-image"
	// ControlPlaneOperatorImageAnnotation is a annotation that allows the specification of the control plane operator image.
	// This is used for development and e2e workflows
	ControlPlaneOperatorImageAnnotation = "hypershift.openshift.io/control-plane-operator-image"
	// RestartDateAnnotation is a annotation that can be used to trigger a rolling restart of all components managed by hypershift.
	// it is important in some situations like CA rotation where components need to be fully restarted to pick up new CAs. It's also
	// important in some recovery situations where a fresh start of the component helps fix symptoms a user might be experiencing.
	RestartDateAnnotation = "hypershift.openshift.io/restart-date"
	// ClusterAPIManagerImage is an annotation that allows the specification of the cluster api manager image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	// no images can be pulled from registries outside of IBM Cloud's official regional registries
	ClusterAPIManagerImage = "hypershift.openshift.io/capi-manager-image"
	// ClusterAutoscalerImage is an annotation that allows the specification of the cluster autoscaler image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	//no images can be pulled from registries outside of IBM Cloud's official regional registries
	ClusterAutoscalerImage = "hypershift.openshift.io/cluster-autoscaler-image"
	// AWSKMSProviderImage is an annotation that allows the specification of the AWS kms provider image.
	// Upstream code located at: https://github.com/kubernetes-sigs/aws-encryption-provider
	AWSKMSProviderImage = "hypershift.openshift.io/aws-kms-provider-image"
	// IBMCloudKMSProviderImage is an annotation that allows the specification of the IBM Cloud kms provider image.
	IBMCloudKMSProviderImage = "hypershift.openshift.io/ibmcloud-kms-provider-image"
	// PortierisImageAnnotation is an annotation that allows the specification of the portieries component
	// (performs container image verification).
	PortierisImageAnnotation = "hypershift.openshift.io/portieris-image"

	// AESCBCKeySecretKey defines the Kubernetes secret key name that contains the aescbc encryption key
	// in the AESCBC secret encryption strategy
	AESCBCKeySecretKey = "key"
	// IBMCloudIAMAPIKeySecretKey defines the Kubernetes secret key name that contains
	// the customer IBMCloud apikey in the unmanaged authentication strategy for IBMCloud KMS secret encryption
	IBMCloudIAMAPIKeySecretKey = "iam_apikey"
	// AWSCredentialsFileSecretKey defines the Kubernetes secret key name that contains
	// the customer AWS credentials in the unmanaged authentication strategy for AWS KMS secret encryption
	AWSCredentialsFileSecretKey = "credentials"

	// ControlPlaneComponent identifies a resource as belonging to a hosted control plane.
	ControlPlaneComponent = "hypershift.openshift.io/control-plane-component"

	// OperatorComponent identifies a component as belonging to the operator.
	OperatorComponent = "hypershift.openshift.io/operator-component"
	// MachineApproverImage is an annotation that allows the specification of the machine approver image.
	// This is a temporary workaround necessary for compliance reasons on the IBM Cloud side:
	// no images can be pulled from registries outside of IBM Cloud's official regional registries
	MachineApproverImage = "hypershift.openshift.io/machine-approver-image"
)

// HostedClusterSpec defines the desired state of HostedCluster
type HostedClusterSpec struct {

	// Release specifies the release image to use for this HostedCluster
	Release Release `json:"release"`

	// +optional
	// +immutable
	FIPS bool `json:"fips"`

	// PullSecret is a pull secret injected into the container runtime of guest
	// workers. It should have an ".dockerconfigjson" key containing the pull secret JSON.
	// +immutable
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// AuditWebhook contains metadata for configuring an audit webhook
	// endpoint for a cluster to process cluster audit events. It references
	// a secret that contains the webhook information for the audit webhook endpoint.
	// It is a secret because if the endpoint has MTLS the kubeconfig will contain client
	// keys. This is currently only supported in IBM Cloud. The kubeconfig needs to be stored
	// in the secret with a secret key name that corresponds to the constant AuditWebhookKubeconfigKey.
	// +optional
	// +immutable
	AuditWebhook *corev1.LocalObjectReference `json:"auditWebhook,omitempty"`

	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	IssuerURL string `json:"issuerURL"`

	// SSHKey is a reference to a Secret containing a single key "id_rsa.pub",
	// whose value is the public part of an SSH key that can be used to access
	// Nodes.
	// +immutable
	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// Networking contains network-specific settings for this cluster
	// +immutable
	Networking ClusterNetworking `json:"networking"`

	// Autoscaling for compute nodes only, does not cover control plane
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	// +immutable
	Platform PlatformSpec `json:"platform"`

	// InfraID is used to identify the cluster in cloud platforms
	// +immutable
	InfraID string `json:"infraID"`

	// DNS configuration for the cluster
	// +immutable
	DNS DNSSpec `json:"dns,omitempty"`

	// Services defines metadata about how control plane services are published
	// in the management cluster.
	// TODO (alberto): include Ignition endpoint here.
	Services []ServicePublishingStrategyMapping `json:"services"`

	// ControllerAvailabilityPolicy specifies an availability policy to apply
	// to critical control plane components.
	// Defaults to SingleReplica when not set.
	// +optional
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// InfrastructureAvailabilityPolicy specifies whether to run infrastructure services that
	// run on the guest cluster nodes in HA mode
	// Defaults to HighlyAvailable when not set
	// +optional
	// +immutable
	InfrastructureAvailabilityPolicy AvailabilityPolicy `json:"infrastructureAvailabilityPolicy,omitempty"`

	// Etcd contains metadata about the etcd cluster the hypershift managed Openshift control plane components
	// use to store data. Changing the ManagementType for the etcd cluster is not supported after initial creation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={managementType: "Managed"}
	// +immutable
	Etcd EtcdSpec `json:"etcd"`

	// Configuration embeds resources that correspond to the openshift configuration API:
	// https://docs.openshift.com/container-platform/4.7/rest_api/config_apis/config-apis-index.html
	// +kubebuilder:validation:Optional
	// +optional
	Configuration *ClusterConfiguration `json:"configuration,omitempty"`

	// ImageContentSources lists sources/repositories for the release-image content.
	// +optional
	// +immutable
	ImageContentSources []ImageContentSource `json:"imageContentSources,omitempty"`

	// SecretEncryption contains metadata about the kubernetes secret encryption strategy being used for the
	// cluster when applicable.
	// +optional
	SecretEncryption *SecretEncryptionSpec `json:"secretEncryption,omitempty"`
}

// ImageContentSource defines a list of sources/repositories that can be used to pull content.
type ImageContentSource struct {
	// Source is the repository that users refer to, e.g. in image pull specifications.
	// +immutable
	Source string `json:"source"`

	// Mirrors is one or more repositories that may also contain the same images.
	// +optional
	// +immutable
	Mirrors []string `json:"mirrors,omitempty"`
}

// ServicePublishingStrategyMapping defines the service being published and  metadata about the publishing strategy.
type ServicePublishingStrategyMapping struct {
	// Service identifies the type of service being published
	// +kubebuilder:validation:Enum=APIServer;OAuthServer;OIDC;Konnectivity;Ignition
	// +immutable
	Service                   ServiceType `json:"service"`
	ServicePublishingStrategy `json:"servicePublishingStrategy"`
}

// ServicePublishingStrategy defines metadata around how a service is published
type ServicePublishingStrategy struct {
	// Type defines the publishing strategy used for the service.
	// +kubebuilder:validation:Enum=LoadBalancer;NodePort;Route;None;S3
	// +immutable
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
	// S3 exoses a service through an S3 bucket
	S3 PublishingStrategyType = "S3"
	// None disables exposing the service
	None PublishingStrategyType = "None"
)

// ServiceType defines what control plane services can be exposed from the management control plane
type ServiceType string

var (
	APIServer    ServiceType = "APIServer"
	Konnectivity ServiceType = "Konnectivity"
	OAuthServer  ServiceType = "OAuthServer"
	OIDC         ServiceType = "OIDC"
	Ignition     ServiceType = "Ignition"
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
	// +immutable
	BaseDomain string `json:"baseDomain"`

	// PublicZoneID is the Hosted Zone ID where all the DNS records that are publicly accessible to
	// the internet exist.
	// +optional
	// +immutable
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// PrivateZoneID is the Hosted Zone ID where all the DNS records that are only available internally
	// to the cluster exist.
	// +optional
	// +immutable
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}

type ClusterNetworking struct {
	// +immutable
	ServiceCIDR string `json:"serviceCIDR"`
	// +immutable
	PodCIDR string `json:"podCIDR"`
	// +immutable
	MachineCIDR string `json:"machineCIDR"`
	// NetworkType specifies the SDN provider used for cluster networking.
	// +kubebuilder:default:="OpenShiftSDN"
	// +immutable
	NetworkType NetworkType `json:"networkType"`

	// APIServer contains advanced network settings for the API server that affect
	// how the APIServer is exposed inside a worker node.
	// +immutable
	APIServer *APIServerNetworking `json:"apiServer,omitempty"`
}

// APIServerNetworking specifies how the APIServer is exposed inside a worker node.
type APIServerNetworking struct {
	// AdvertiseAddress is the address that workers will use to talk to the
	// API server. This is an address associated with the loopback adapter of
	// each worker. If not specified, 172.20.0.1 is used.
	AdvertiseAddress *string `json:"advertiseAddress,omitempty"`

	// Port is the port at which the APIServer is exposed inside a worker node
	// Other pods using host networking cannot listen on this port. If not
	// specified, 6443 is used.
	Port *int32 `json:"port,omitempty"`
}

// NetworkType specifies the SDN provider used for cluster networking.
// +kubebuilder:validation:Enum=OpenShiftSDN;Calico
type NetworkType string

const (
	// OpenShiftSDN specifies OpenshiftSDN as the SDN provider
	OpenShiftSDN NetworkType = "OpenShiftSDN"

	// Calico specifies Calico as the SDN provider
	Calico NetworkType = "Calico"
)

// PlatformType is a specific supported infrastructure provider.
// +kubebuilder:validation:Enum=AWS;None;IBMCloud
type PlatformType string

const (
	// AWSPlatformType represents Amazon Web Services infrastructure.
	AWSPlatform PlatformType = "AWS"

	NonePlatform PlatformType = "None"

	IBMCloudPlatform PlatformType = "IBMCloud"
)

type PlatformSpec struct {
	// Type is the underlying infrastructure provider for the cluster.
	//
	// +unionDiscriminator
	// +immutable
	Type PlatformType `json:"type"`

	// AWS contains AWS-specific settings for the HostedCluster
	// +optional
	// +immutable
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

type AWSEndpointAccessType string

const (
	// Public endpoint access allows public kube-apiserver access and public node communication with the control plane
	Public AWSEndpointAccessType = "Public"

	// PublicAndPrivate endpoint access allows public kube-apiserver access and private node communication with the control plane
	PublicAndPrivate AWSEndpointAccessType = "PublicAndPrivate"

	// Private endpoint access allows only private kube-apiserver access and private node communication with the control plane
	Private AWSEndpointAccessType = "Private"
)

type AWSPlatformSpec struct {
	// Region is the AWS region for the cluster.
	// This is used by CRs that are consumed by OCP Operators.
	// E.g cluster-infrastructure-02-config.yaml and install-config.yaml
	// This is also used by nodePools to fetch the default boot AMI in a given payload.
	// +immutable
	Region string `json:"region"`

	// CloudProviderConfig is used to generate the ConfigMap with the cloud config consumed
	// by the Control Plane components.
	// +optional
	// +immutable
	CloudProviderConfig *AWSCloudProviderConfig `json:"cloudProviderConfig,omitempty"`

	// ServiceEndpoints list contains custom endpoints which will override default
	// service endpoint of AWS Services.
	// There must be only one ServiceEndpoint for a service.
	// +optional
	// +immutable
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`

	// +immutable
	Roles []AWSRoleCredentials `json:"roles,omitempty"`

	// KubeCloudControllerCreds is a reference to a secret containing cloud
	// credentials with permissions matching the Kube cloud controller policy.
	// The secret should have exactly one key, `credentials`, whose value is
	// an AWS credentials file.
	// +immutable
	KubeCloudControllerCreds corev1.LocalObjectReference `json:"kubeCloudControllerCreds"`

	// NodePoolManagementCreds is a reference to a secret containing cloud
	// credentials with permissions matching the noe pool management policy.
	// The secret should have exactly one key, `credentials`, whose value is
	// an AWS credentials file.
	// +immutable
	NodePoolManagementCreds corev1.LocalObjectReference `json:"nodePoolManagementCreds"`

	// resourceTags is a list of additional tags to apply to AWS resources created for the cluster.
	// See https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for information on tagging AWS resources.
	// AWS supports a maximum of 50 tags per resource. OpenShift reserves 25 tags for its use, leaving 25 tags
	// available for the user.
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// EndpointAccess determines if cluster endpoints are public and/or private
	// +kubebuilder:validation:Enum=Public;PublicAndPrivate;Private
	// +kubebuilder:default=Public
	// +optional
	EndpointAccess AWSEndpointAccessType `json:"endpointAccess,omitempty"`
}

// AWSResourceTag is a tag to apply to AWS resources created for the cluster.
type AWSResourceTag struct {
	// key is the key of the tag
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	// +required
	Key string `json:"key"`
	// value is the value of the tag.
	// Some AWS service do not support empty values. Since tags are added to resources in many services, the
	// length of the tag value must meet the requirements of all services.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	// +required
	Value string `json:"value"`
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
	// +immutable
	ManagementType EtcdManagementType `json:"managementType"`

	// Managed provides metadata that defines how the hypershift controllers manage the etcd cluster
	// +optional
	// +immutable
	Managed *ManagedEtcdSpec `json:"managed,omitempty"`

	// Unmanaged provides metadata that enables the Openshift controllers to connect to the external etcd cluster
	// +optional
	// +immutable
	Unmanaged *UnmanagedEtcdSpec `json:"unmanaged,omitempty"`
}

type ManagedEtcdSpec struct {
	// Storage configures how etcd data is persisted.
	Storage ManagedEtcdStorageSpec `json:"storage"`
}

// +kubebuilder:validation:Enum=PersistentVolume
type ManagedEtcdStorageType string

const (
	// PersistentVolumeEtcdStorage uses PersistentVolumes for etcd storage.
	PersistentVolumeEtcdStorage ManagedEtcdStorageType = "PersistentVolume"
)

var (
	DefaultPersistentVolumeEtcdStorageSize resource.Quantity = resource.MustParse("4Gi")
)

// ManagedEtcdStorageSpec describes the storage configuration for etcd data.
type ManagedEtcdStorageSpec struct {
	// Type is the kind of persistent storage implementation to use for etcd.
	//
	// +kubebuilder:validation:Required
	// +immutable
	// +unionDiscriminator
	Type ManagedEtcdStorageType `json:"type"`

	// PersistentVolume is the configuration for PersistentVolume etcd storage.
	// With this implementation, a PersistentVolume will be allocated for every
	// etcd member (either 1 or 3 depending on the HostedCluster control plane
	// availability configuration).
	//
	// +optional
	PersistentVolume *PersistentVolumeEtcdStorageSpec `json:"persistentVolume,omitempty"`
}

// PersistentVolumeEtcdStorageSpec is the configuration for PersistentVolume
// etcd storage.
type PersistentVolumeEtcdStorageSpec struct {
	// StorageClassName is the StorageClass of the data volume for each etcd member.
	//
	// See https://kubernetes.io/docs/concepts/storage/persistent-volumes#class-1.
	//
	// +optional
	// +immutable
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Size is the minimum size of the data volume for each etcd member.
	//
	// +optional
	// +kubebuilder:default="4Gi"
	Size *resource.Quantity `json:"size,omitempty"`
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
	// ClientSecret refers to a secret for client MTLS authentication with the etcd cluster
	// The CA must be stored at secret key etcd-client-ca.crt.
	// The client cert must be stored at secret key etcd-client.crt.
	// The client key must be stored at secret key etcd-client.key.
	ClientSecret corev1.LocalObjectReference `json:"clientSecret"`
}

// SecretEncryptionType defines the type of kube secret encryption being used.
// +kubebuilder:validation:Enum=kms;aescbc
type SecretEncryptionType string

const (
	// KMS integrates with a cloud provider's key management service to do secret encryption
	KMS SecretEncryptionType = "kms"
	// AESCBC uses AES-CBC with PKCS#7 padding to do secret encryption
	AESCBC SecretEncryptionType = "aescbc"
)

// SecretEncryptionSpec contains metadata about the kubernetes secret encryption strategy being used for the
// cluster when applicable.
type SecretEncryptionSpec struct {
	// Type defines the type of kube secret encryption being used
	// +unionDiscriminator
	Type SecretEncryptionType `json:"type"`

	// KMS defines metadata about the kms secret encryption strategy
	// +optional
	KMS *KMSSpec `json:"kms,omitempty"`

	// AESCBC defines metadata about the AESCBC secret encryption strategy
	// +optional
	AESCBC *AESCBCSpec `json:"aescbc,omitempty"`
}

// KMSProvider defines the supported KMS providers
// +kubebuilder:validation:Enum=IBMCloud;AWS
type KMSProvider string

const (
	IBMCloud KMSProvider = "IBMCloud"
	AWS      KMSProvider = "AWS"
)

// KMSSpec defines metadata about the kms secret encryption strategy
type KMSSpec struct {
	// Provider defines the KMS provider
	// +unionDiscriminator
	Provider KMSProvider `json:"provider"`
	// IBMCloud defines metadata for the IBM Cloud KMS encryption strategy
	// +optional
	IBMCloud *IBMCloudKMSSpec `json:"ibmcloud,omitempty"`
	// AWS defines metadata about the configuration of the AWS KMS Secret Encryption provider
	// +optional
	AWS *AWSKMSSpec `json:"aws,omitempty"`
}

// IBMCloudKMSSpec defines metadata for the IBM Cloud KMS encryption strategy
type IBMCloudKMSSpec struct {
	// Region is the IBM Cloud region
	Region string `json:"region"`
	// Auth defines metadata for how authentication is done with IBM Cloud KMS
	Auth IBMCloudKMSAuthSpec `json:"auth"`
	// KeyList defines the list of keys used for data encryption
	KeyList []IBMCloudKMSKeyEntry `json:"keyList"`
}

// IBMCloudKMSKeyEntry defines metadata for an IBM Cloud KMS encryption key
type IBMCloudKMSKeyEntry struct {
	// CRKID is the customer rook key id
	CRKID string `json:"crkID"`
	// InstanceID is the id for the key protect instance
	InstanceID string `json:"instanceID"`
	// CorrelationID is an identifier used to track all api call usage from hypershift
	CorrelationID string `json:"correlationID"`
	// URL is the url to call key protect apis over
	// +kubebuilder:validation:Pattern=`^https://`
	URL string `json:"url"`
	// KeyVersion is a unique number associated with the key. The number increments whenever a new
	// key is enabled for data encryption.
	KeyVersion int `json:"keyVersion"`
}

// IBMCloudKMSAuthSpec defines metadata for how authentication is done with IBM Cloud KMS
type IBMCloudKMSAuthSpec struct {
	// Type defines the IBM Cloud KMS authentication strategy
	// +unionDiscriminator
	Type IBMCloudKMSAuthType `json:"type"`
	// Unmanaged defines the auth metadata the customer provides to interact with IBM Cloud KMS
	// +optional
	Unmanaged *IBMCloudKMSUnmanagedAuthSpec `json:"unmanaged,omitempty"`
	// Managed defines metadata around the service to service authentication strategy for the IBM Cloud
	// KMS system (all provider managed).
	// +optional
	Managed *IBMCloudKMSManagedAuthSpec `json:"managed,omitempty"`
}

// IBMCloudKMSAuthType defines the IBM Cloud KMS authentication strategy
// +kubebuilder:validation:Enum=Managed;Unmanaged
type IBMCloudKMSAuthType string

const (
	// IBMCloudKMSManagedAuth defines the KMS authentication strategy where the IKS/ROKS platform uses
	// service to service auth to call IBM Cloud KMS APIs (no customer credentials requried)
	IBMCloudKMSManagedAuth IBMCloudKMSAuthType = "Managed"
	// IBMCloudKMSUnmanagedAuth defines the KMS authentication strategy where a customer supplies IBM Cloud
	// authentication to interact with IBM Cloud KMS APIs
	IBMCloudKMSUnmanagedAuth IBMCloudKMSAuthType = "Unmanaged"
)

// IBMCloudKMSUnmanagedAuthSpec defines the auth metadata the customer provides to interact with IBM Cloud KMS
type IBMCloudKMSUnmanagedAuthSpec struct {
	// Credentials should reference a secret with a key field of IBMCloudIAMAPIKeySecretKey that contains a apikey to
	// call IBM Cloud KMS APIs
	Credentials corev1.LocalObjectReference `json:"credentials"`
}

// IBMCloudKMSManagedAuthSpec defines metadata around the service to service authentication strategy for the IBM Cloud
// KMS system (all provider managed).
type IBMCloudKMSManagedAuthSpec struct {
}

// AWSKMSSpec defines metadata about the configuration of the AWS KMS Secret Encryption provider
type AWSKMSSpec struct {
	// Region contains the AWS region
	Region string `json:"region"`
	// ActiveKey defines the active key used to encrypt new secrets
	ActiveKey AWSKMSKeyEntry `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AWSKMSKeyEntry `json:"backupKey,omitempty"`
	// Auth defines metadata about the management of credentials used to interact with AWS KMS
	Auth AWSKMSAuthSpec `json:"auth"`
}

// AWSKMSAuthSpec defines metadata about the management of credentials used to interact with AWS KMS
type AWSKMSAuthSpec struct {
	// Credentials contains the name of the secret that holds the aws credentials that can be used
	// to make the necessary KMS calls. It should at key AWSCredentialsFileSecretKey contain the
	// aws credentials file that can be used to configure AWS SDKs
	Credentials corev1.LocalObjectReference `json:"credentials"`
}

// AWSKMSKeyEntry defines metadata to locate the encryption key in AWS
type AWSKMSKeyEntry struct {
	// ARN is the Amazon Resource Name for the encryption key
	// +kubebuilder:validation:Pattern=`^arn:`
	ARN string `json:"arn"`
}

// AESCBCSpec defines metadata about the AESCBC secret encryption strategy
type AESCBCSpec struct {
	// ActiveKey defines the active key used to encrypt new secrets
	ActiveKey corev1.LocalObjectReference `json:"activeKey"`
	// BackupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *corev1.LocalObjectReference `json:"backupKey,omitempty"`
}

const (
	// HostedClusterAvailable indicates whether the HostedCluster has a healthy
	// control plane.
	HostedClusterAvailable ConditionType = "Available"

	// IgnitionEndpointAvailable indicates whether the ignition server for the
	// HostedCluster is available to handle ignition requests.
	IgnitionEndpointAvailable ConditionType = "IgnitionEndpointAvailable"

	// UnmanagedEtcdAvailable indicates whether a user-managed etcd cluster is
	// healthy.
	UnmanagedEtcdAvailable ConditionType = "UnmanagedEtcdAvailable"

	// ValidHostedClusterConfiguration indicates (if status is true) that the
	// ClusterConfiguration specified for the HostedCluster is valid.
	ValidHostedClusterConfiguration ConditionType = "ValidConfiguration"

	// SupportedHostedCluster indicates whether a HostedCluster is supported by
	// the current configuration of the hypershift-operator.
	// e.g. If HostedCluster requests endpointAcess Private but the hypershift-operator
	// is running on a management cluster outside AWS or is not configured with AWS
	// credentials, the HostedCluster is not supported.
	SupportedHostedCluster ConditionType = "SupportedHostedCluster"
)

const (
	IgnitionServerDeploymentAsExpectedReason    = "IgnitionServerDeploymentAsExpected"
	IgnitionServerDeploymentStatusUnknownReason = "IgnitionServerDeploymentStatusUnknown"
	IgnitionServerDeploymentNotFoundReason      = "IgnitionServerDeploymentNotFound"
	IgnitionServerDeploymentUnavailableReason   = "IgnitionServerDeploymentUnavailable"

	HostedClusterAsExpectedReason          = "HostedClusterAsExpected"
	HostedClusterUnhealthyComponentsReason = "UnhealthyControlPlaneComponents"
	InvalidConfigurationReason             = "InvalidConfiguration"

	UnsupportedHostedClusterReason = "UnsupportedHostedCluster"

	UnmanagedEtcdStatusUnknownReason = "UnmanagedEtcdStatusUnknown"
	UnmanagedEtcdMisconfiguredReason = "UnmanagedEtcdMisconfigured"
	UnmanagedEtcdAsExpected          = "UnmanagedEtcdAsExpected"

	InsufficientClusterCapabilitiesReason = "InsufficientClusterCapabilities"
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

// ClusterConfiguration contains global configuration for a HostedCluster.
type ClusterConfiguration struct {
	// SecretRefs holds references to secrets used in configuration entries
	// so that they can be properly synced by the hypershift operator.
	// +kubebuilder:validation:Optional
	// +optional
	SecretRefs []corev1.LocalObjectReference `json:"secretRefs,omitempty"`

	// ConfigMapRefs holds references to configmaps used in configuration entries
	// so that they can be properly synced by the hypershift operator.
	// +kubebuilder:validation:Optional
	// +optional
	ConfigMapRefs []corev1.LocalObjectReference `json:"configMapRefs,omitempty"`

	// Items embeds the configuration resource
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Optional
	// +optional
	Items []runtime.RawExtension `json:"items,omitempty"`
}

// +genclient

// HostedCluster is the primary representation of a HyperShift cluster and encapsulates
// the control plane and common data plane configuration. Creating a HostedCluster
// results in a fully functional OpenShift control plane with no attached nodes.
// To support workloads (e.g. pods), a HostedCluster may have one or more associated
// NodePool resources.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hostedclusters,shortName=hc;hcs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version.history[?(@.state==\"Completed\")].version",description="Version"
// +kubebuilder:printcolumn:name="KubeConfig",type="string",JSONPath=".status.kubeconfig.name",description="KubeConfig Secret"
// +kubebuilder:printcolumn:name="Progress",type="string",JSONPath=".status.version.history[?(@.state!=\"\")].state",description="Progress"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].status",description="Available"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type==\"Available\")].reason",description="Reason"
type HostedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the HostedCluster.
	Spec HostedClusterSpec `json:"spec,omitempty"`

	// Status is the latest observed status of the HostedCluster.
	Status HostedClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// HostedClusterList contains a list of HostedCluster
type HostedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedCluster `json:"items"`
}
