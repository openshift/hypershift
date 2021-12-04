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

// HostedClusterSpec is the desired behavior of a HostedCluster.
type HostedClusterSpec struct {
	// Release specifies the desired OCP release payload for the hosted cluster.
	//
	// Updating this field will trigger a rollout of the control plane. The
	// behavior of the rollout will be driven by the ControllerAvailabilityPolicy
	// and InfrastructureAvailabilityPolicy.
	Release Release `json:"release"`

	// InfraID is a globally unique identifier for the cluster. This identifier
	// will be used to associate various cloud resources with the HostedCluster
	// and its associated NodePools.
	//
	// TODO(dan): consider moving this to .platform.aws.infraID
	//
	// +immutable
	InfraID string `json:"infraID"`

	// Platform specifies the underlying infrastructure provider for the cluster
	// and is used to configure platform specific behavior.
	//
	// +immutable
	Platform PlatformSpec `json:"platform"`

	// ControllerAvailabilityPolicy specifies the availability policy applied to
	// critical control plane components. The default value is SingleReplica.
	//
	// +optional
	// +immutable
	ControllerAvailabilityPolicy AvailabilityPolicy `json:"controllerAvailabilityPolicy,omitempty"`

	// InfrastructureAvailabilityPolicy specifies the availability policy applied
	// to infrastructure services which run on cluster nodes. The default value is
	// HighlyAvailable.
	//
	// +optional
	// +immutable
	InfrastructureAvailabilityPolicy AvailabilityPolicy `json:"infrastructureAvailabilityPolicy,omitempty"`

	// DNS specifies DNS configuration for the cluster.
	//
	// +immutable
	DNS DNSSpec `json:"dns,omitempty"`

	// Networking specifies network configuration for the cluster.
	//
	// +immutable
	Networking ClusterNetworking `json:"networking"`

	// Autoscaling specifies auto-scaling behavior that applies to all NodePools
	// associated with the control plane.
	//
	// +optional
	Autoscaling ClusterAutoscaling `json:"autoscaling,omitempty"`

	// Etcd specifies configuration for the control plane etcd cluster. The
	// default ManagementType is Managed. Once set, the ManagementType cannot be
	// changed.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={managementType: "Managed"}
	// +immutable
	Etcd EtcdSpec `json:"etcd"`

	// Services specifies how individual control plane services are published from
	// the hosting cluster of the control plane.
	//
	// If a given service is not present in this list, it will be exposed publicly
	// by default.
	Services []ServicePublishingStrategyMapping `json:"services"`

	// PullSecret references a pull secret to be injected into the container
	// runtime of all cluster nodes. The secret must have a key named
	// ".dockerconfigjson" whose value is the pull secret JSON.
	//
	// +immutable
	PullSecret corev1.LocalObjectReference `json:"pullSecret"`

	// SSHKey references an SSH key to be injected into all cluster node sshd
	// servers. The secret must have a single key "id_rsa.pub" whose value is the
	// public part of an SSH key.
	//
	// +immutable
	SSHKey corev1.LocalObjectReference `json:"sshKey"`

	// IssuerURL is an OIDC issuer URL which is used as the issuer in all
	// ServiceAccount tokens generated by the control plane API server. The
	// default value is kubernetes.default.svc, which only works for in-cluster
	// validation.
	//
	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	IssuerURL string `json:"issuerURL"`

	// Configuration specifies configuration for individual OCP components in the
	// cluster, represented as embedded resources that correspond to the openshift
	// configuration API.
	//
	// +kubebuilder:validation:Optional
	// +optional
	Configuration *ClusterConfiguration `json:"configuration,omitempty"`

	// AuditWebhook contains metadata for configuring an audit webhook endpoint
	// for a cluster to process cluster audit events. It references a secret that
	// contains the webhook information for the audit webhook endpoint. It is a
	// secret because if the endpoint has mTLS the kubeconfig will contain client
	// keys. The kubeconfig needs to be stored in the secret with a secret key
	// name that corresponds to the constant AuditWebhookKubeconfigKey.
	//
	// This field is currently only supported on the IBMCloud platform.
	//
	// +optional
	// +immutable
	AuditWebhook *corev1.LocalObjectReference `json:"auditWebhook,omitempty"`

	// ImageContentSources specifies image mirrors that can be used by cluster
	// nodes to pull content.
	//
	// +optional
	// +immutable
	ImageContentSources []ImageContentSource `json:"imageContentSources,omitempty"`

	// SecretEncryption specifies a Kubernetes secret encryption strategy for the
	// control plane.
	//
	// +optional
	SecretEncryption *SecretEncryptionSpec `json:"secretEncryption,omitempty"`

	// FIPS indicates whether this cluster's nodes will be running in FIPS mode.
	// If set to true, the control plane's ignition server will be configured to
	// expect that nodes joining the cluster will be FIPS-enabled.
	//
	// +optional
	// +immutable
	FIPS bool `json:"fips"`
}

// ImageContentSource specifies image mirrors that can be used by cluster nodes
// to pull content. For cluster workloads, if a container image registry host of
// the pullspec matches Source then one of the Mirrors are substituted as hosts
// in the pullspec and tried in order to fetch the image.
type ImageContentSource struct {
	// Source is the repository that users refer to, e.g. in image pull
	// specifications.
	//
	// +immutable
	Source string `json:"source"`

	// Mirrors are one or more repositories that may also contain the same images.
	//
	// +optional
	// +immutable
	Mirrors []string `json:"mirrors,omitempty"`
}

// ServicePublishingStrategyMapping specifies how individual control plane
// services are published from the hosting cluster of a control plane.
type ServicePublishingStrategyMapping struct {
	// Service identifies the type of service being published.
	//
	// +kubebuilder:validation:Enum=APIServer;OAuthServer;OIDC;Konnectivity;Ignition
	// +immutable
	Service ServiceType `json:"service"`

	// ServicePublishingStrategy specifies how to publish Service.
	ServicePublishingStrategy `json:"servicePublishingStrategy"`
}

// ServicePublishingStrategy specfies how to publish a ServiceType.
type ServicePublishingStrategy struct {
	// Type is the publishing strategy used for the service.
	//
	// +kubebuilder:validation:Enum=LoadBalancer;NodePort;Route;None;S3
	// +immutable
	Type PublishingStrategyType `json:"type"`

	// NodePort configures exposing a service using a NodePort.
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

// ServiceType defines what control plane services can be exposed from the
// management control plane.
type ServiceType string

var (
	// APIServer is the control plane API server.
	APIServer ServiceType = "APIServer"

	// Konnectivity is the control plane Konnectivity networking service.
	Konnectivity ServiceType = "Konnectivity"

	// OAuthServer is the control plane OAuth service.
	OAuthServer ServiceType = "OAuthServer"

	// OIDC is the control plane OIDC service.
	OIDC ServiceType = "OIDC"

	// Ignition is the control plane ignition service for nodes.
	Ignition ServiceType = "Ignition"
)

// NodePortPublishingStrategy specifies a NodePort used to expose a service.
type NodePortPublishingStrategy struct {
	// Address is the host/ip that the NodePort service is exposed over.
	Address string `json:"address"`

	// Port is the port of the NodePort service. If <=0, the port is dynamically
	// assigned when the service is created.
	Port int32 `json:"port,omitempty"`
}

// DNSSpec specifies the DNS configuration in the cluster.
type DNSSpec struct {
	// BaseDomain is the base domain of the cluster.
	//
	// +immutable
	BaseDomain string `json:"baseDomain"`

	// PublicZoneID is the Hosted Zone ID where all the DNS records that are
	// publicly accessible to the internet exist.
	//
	// +optional
	// +immutable
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// PrivateZoneID is the Hosted Zone ID where all the DNS records that are only
	// available internally to the cluster exist.
	//
	// +optional
	// +immutable
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}

// ClusterNetworking specifies network configuration for a cluster.
type ClusterNetworking struct {
	// ServiceCIDR is...
	//
	// TODO(dan): document it
	//
	// +immutable
	ServiceCIDR string `json:"serviceCIDR"`

	// PodCIDR is...
	//
	// TODO(dan): document it
	//
	// +immutable
	PodCIDR string `json:"podCIDR"`

	// MachineCIDR is...
	//
	// TODO(dan): document it
	//
	// +immutable
	MachineCIDR string `json:"machineCIDR"`

	// NetworkType specifies the SDN provider used for cluster networking.
	//
	// +kubebuilder:default:="OpenShiftSDN"
	// +immutable
	NetworkType NetworkType `json:"networkType"`

	// APIServer contains advanced network settings for the API server that affect
	// how the APIServer is exposed inside a cluster node.
	//
	// +immutable
	APIServer *APIServerNetworking `json:"apiServer,omitempty"`
}

// APIServerNetworking specifies how the APIServer is exposed inside a cluster
// node.
type APIServerNetworking struct {
	// AdvertiseAddress is the address that nodes will use to talk to the API
	// server. This is an address associated with the loopback adapter of each
	// node. If not specified, 172.20.0.1 is used.
	AdvertiseAddress *string `json:"advertiseAddress,omitempty"`

	// Port is the port at which the APIServer is exposed inside a node. Other
	// pods using host networking cannot listen on this port. If not specified,
	// 6443 is used.
	Port *int32 `json:"port,omitempty"`
}

// NetworkType specifies the SDN provider used for cluster networking.
//
// +kubebuilder:validation:Enum=OpenShiftSDN;Calico
type NetworkType string

const (
	// OpenShiftSDN specifies OpenshiftSDN as the SDN provider
	OpenShiftSDN NetworkType = "OpenShiftSDN"

	// Calico specifies Calico as the SDN provider
	Calico NetworkType = "Calico"
)

// PlatformType is a specific supported infrastructure provider.
//
// +kubebuilder:validation:Enum=AWS;None;IBMCloud
type PlatformType string

const (
	// AWSPlatform represents Amazon Web Services infrastructure.
	AWSPlatform PlatformType = "AWS"

	// NonePlatform represents user supplied (e.g. bare metal) infrastructure.
	NonePlatform PlatformType = "None"

	// IBMCloudPlatform represents IBM Cloud infrastructure.
	IBMCloudPlatform PlatformType = "IBMCloud"
)

// PlatformSpec specifies the underlying infrastructure provider for the cluster
// and is used to configure platform specific behavior.
type PlatformSpec struct {
	// Type is the type of infrastructure provider for the cluster.
	//
	// +unionDiscriminator
	// +immutable
	Type PlatformType `json:"type"`

	// AWS specifies configuration for clusters running on Amazon Web Services.
	//
	// +optional
	// +immutable
	AWS *AWSPlatformSpec `json:"aws,omitempty"`

	// IBMCloud defines IBMCloud specific settings for components
	IBMCloud *IBMCloudPlatformSpec `json:"ibmcloud,omitempty"`
}

// IBMCloudIAASProvider is a specific supported infrastructure provider within IBM Cloud.
// +kubebuilder:validation:Enum=upi;g2
type IBMCloudIAASProvider string

const (
	// UPI is user provided infrastructure. This is used with the IBMCloud Satellite offering. Users add hosts and then
	// use apis to assign their hosts to a given cluster in this mode.
	UPI IBMCloudIAASProvider = "upi"
	// VPCGEN2 is VPC Gen 2 within IBM Cloud https://cloud.ibm.com/docs/vpc.
	VPCGEN2 IBMCloudIAASProvider = "g2"
)

// IBMCloudPlatformSpec defines IBMCloud specific settings for components
type IBMCloudPlatformSpec struct {
	// IAASProvider is a specific supported infrastructure provider within IBM Cloud.
	IAASProvider IBMCloudIAASProvider `json:"iaasProvider,omitempty"`
}

// AWSCloudProviderConfig specifies AWS networking configuration.
type AWSCloudProviderConfig struct {
	// Subnet is the subnet to use for control plane cloud resources.
	//
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

	// Zone is the availability zone where control plane cloud resources are
	// created.
	//
	// +optional
	Zone string `json:"zone,omitempty"`

	// VPC is the VPC to use for control plane cloud resources.
	VPC string `json:"vpc"`
}

// AWSEndpointAccessType specifies the publishing scope of cluster endpoints.
type AWSEndpointAccessType string

const (
	// Public endpoint access allows public API server access and public node
	// communication with the control plane.
	Public AWSEndpointAccessType = "Public"

	// PublicAndPrivate endpoint access allows public API server access and
	// private node communication with the control plane.
	PublicAndPrivate AWSEndpointAccessType = "PublicAndPrivate"

	// Private endpoint access allows only private API server access and private
	// node communication with the control plane.
	Private AWSEndpointAccessType = "Private"
)

// AWSPlatformSpec specifies configuration for clusters running on Amazon Web Services.
type AWSPlatformSpec struct {
	// Region is the AWS region in which the cluster resides. This configures the
	// OCP control plane cloud integrations, and is used by NodePool to resolve
	// the correct boot AMI for a given release.
	//
	// +immutable
	Region string `json:"region"`

	// CloudProviderConfig specifies AWS networking configuration for the control
	// plane.
	//
	// TODO(dan): should this be named AWSNetworkConfig?
	//
	// +optional
	// +immutable
	CloudProviderConfig *AWSCloudProviderConfig `json:"cloudProviderConfig,omitempty"`

	// ServiceEndpoints specifies optional custom endpoints which will override
	// the default service endpoint of specific AWS Services.
	//
	// There must be only one ServiceEndpoint for a given service name.
	//
	// +optional
	// +immutable
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`

	// Roles must contain exactly 3 entries representing the locators for roles
	// supporting the following OCP services:
	//
	// - openshift-ingress-operator/cloud-credentials
	// - openshift-image-registry/installer-cloud-credentials
	//  -openshift-cluster-csi-drivers/ebs-cloud-credentials
	//
	// Each role has unique permission requirements whose documentation is TBD.
	//
	// TODO(dan): revisit this field; it's really 3 required fields with specific content requirements
	//
	// +immutable
	Roles []AWSRoleCredentials `json:"roles,omitempty"`

	// KubeCloudControllerCreds is a reference to a secret containing cloud
	// credentials with permissions matching the cloud controller policy. The
	// secret should have exactly one key, `credentials`, whose value is an AWS
	// credentials file.
	//
	// TODO(dan): document the "cloud controller policy"
	//
	// +immutable
	KubeCloudControllerCreds corev1.LocalObjectReference `json:"kubeCloudControllerCreds"`

	// NodePoolManagementCreds is a reference to a secret containing cloud
	// credentials with permissions matching the node pool management policy. The
	// secret should have exactly one key, `credentials`, whose value is an AWS
	// credentials file.
	//
	// TODO(dan): document the "node pool management policy"
	//
	// +immutable
	NodePoolManagementCreds corev1.LocalObjectReference `json:"nodePoolManagementCreds"`

	// ControlPlaneOperatorCreds is a reference to a secret containing cloud
	// credentials with permissions matching the control-plane-operator policy.
	// The secret should have exactly one key, `credentials`, whose value is
	// an AWS credentials file.
	//
	// TODO(dan): document the "control plane operator policy"
	//
	// +immutable
	ControlPlaneOperatorCreds corev1.LocalObjectReference `json:"controlPlaneOperatorCreds"`

	// ResourceTags is a list of additional tags to apply to AWS resources created
	// for the cluster. See
	// https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for
	// information on tagging AWS resources. AWS supports a maximum of 50 tags per
	// resource. OpenShift reserves 25 tags for its use, leaving 25 tags available
	// for the user.
	//
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// EndpointAccess specifies the publishing scope of cluster endpoints. The
	// default is Public.
	//
	// +kubebuilder:validation:Enum=Public;PublicAndPrivate;Private
	// +kubebuilder:default=Public
	// +optional
	EndpointAccess AWSEndpointAccessType `json:"endpointAccess,omitempty"`
}

// AWSResourceTag is a tag to apply to AWS resources created for the cluster.
type AWSResourceTag struct {
	// Key is the key of the tag.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	Key string `json:"key"`
	// Value is the value of the tag.
	//
	// Some AWS service do not support empty values. Since tags are added to
	// resources in many services, the length of the tag value must meet the
	// requirements of all services.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
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

// Release represents the metadata for an OCP release payload image.
type Release struct {
	// Image is the image pullspec of an OCP release payload image.
	//
	// +kubebuilder:validation:Pattern=^(\w+\S+)$
	Image string `json:"image"`
}

// ClusterAutoscaling specifies auto-scaling behavior that applies to all
// NodePools associated with a control plane.
type ClusterAutoscaling struct {
	// MaxNodesTotal is the maximum allowable number of nodes across all NodePools
	// for a HostedCluster. The autoscaler will not grow the cluster beyond this
	// number.
	//
	// +kubebuilder:validation:Minimum=0
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"`

	// MaxPodGracePeriod is the maximum seconds to wait for graceful pod
	// termination before scaling down a NodePool. The default is 600 seconds.
	//
	// +kubebuilder:validation:Minimum=0
	MaxPodGracePeriod *int32 `json:"maxPodGracePeriod,omitempty"`

	// MaxNodeProvisionTime is the maximum time to wait for node provisioning
	// before considering the provisioning to be unsuccessful, expressed as a Go
	// duration string. The default is 15 minutes.
	//
	// +kubebuilder:validation:Pattern=^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$
	MaxNodeProvisionTime string `json:"maxNodeProvisionTime,omitempty"`

	// PodPriorityThreshold enables users to schedule "best-effort" pods, which
	// shouldn't trigger autoscaler actions, but only run when there are spare
	// resources available. The default is -10.
	//
	// See the following for more details:
	// https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-does-cluster-autoscaler-work-with-pod-priority-and-preemption
	//
	// +optional
	PodPriorityThreshold *int32 `json:"podPriorityThreshold,omitempty"`
}

// EtcdManagementType is a enum specifying the strategy for managing the cluster's etcd instance
// +kubebuilder:validation:Enum=Managed;Unmanaged
type EtcdManagementType string

const (
	// Managed means HyperShift should provision and operator the etcd cluster
	// automatically.
	Managed EtcdManagementType = "Managed"

	// Unmanaged means HyperShift will not provision or manage the etcd cluster,
	// and the user is responsible for doing so.
	Unmanaged EtcdManagementType = "Unmanaged"
)

// EtcdSpec specifies configuration for a control plane etcd cluster.
type EtcdSpec struct {
	// ManagementType defines how the etcd cluster is managed.
	//
	// +unionDiscriminator
	// +immutable
	ManagementType EtcdManagementType `json:"managementType"`

	// Managed specifies the behavior of an etcd cluster managed by HyperShift.
	//
	// +optional
	// +immutable
	Managed *ManagedEtcdSpec `json:"managed,omitempty"`

	// Unmanaged specifies configuration which enables the control plane to
	// integrate with an eternally managed etcd cluster.
	//
	// +optional
	// +immutable
	Unmanaged *UnmanagedEtcdSpec `json:"unmanaged,omitempty"`
}

// ManagedEtcdSpec specifies the behavior of an etcd cluster managed by
// HyperShift.
type ManagedEtcdSpec struct {
	// Storage specifies how etcd data is persisted.
	Storage ManagedEtcdStorageSpec `json:"storage"`
}

// ManagedEtcdStorageType is a storage type for an etcd cluster.
//
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

// UnmanagedEtcdSpec specifies configuration which enables the control plane to
// integrate with an eternally managed etcd cluster.
type UnmanagedEtcdSpec struct {
	// Endpoint is the full etcd cluster client endpoint URL. For example:
	//
	//     https://etcd-client:2379
	//
	// If the URL uses an HTTPS scheme, the TLS field is required.
	//
	// +kubebuilder:validation:Pattern=`^https://`
	Endpoint string `json:"endpoint"`

	// TLS specifies TLS configuration for HTTPS etcd client endpoints.
	TLS EtcdTLSConfig `json:"tls"`
}

// EtcdTLSConfig specifies TLS configuration for HTTPS etcd client endpoints.
type EtcdTLSConfig struct {
	// ClientSecret refers to a secret for client mTLS authentication with the etcd cluster. It
	// may have the following key/value pairs:
	//
	//     etcd-client-ca.crt: Certificate Authority value
	//     etcd-client.crt: Client certificate value
	//     etcd-client.key: Client certificate key value
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

// HostedClusterStatus is the latest observed status of a HostedCluster.
type HostedClusterStatus struct {
	// Version is the status of the release version applied to the
	// HostedCluster.
	// +optional
	Version *ClusterVersionStatus `json:"version,omitempty"`

	// KubeConfig is a reference to the secret containing the default kubeconfig
	// for the cluster.
	// +optional
	KubeConfig *corev1.LocalObjectReference `json:"kubeconfig,omitempty"`

	// KubeadminPassword is a reference to the secret that contains the initial
	// kubeadmin user password for the guest cluster.
	// +optional
	KubeadminPassword *corev1.LocalObjectReference `json:"kubeadminPassword,omitempty"`

	// IgnitionEndpoint is the endpoint injected in the ign config userdata.
	// It exposes the config for instances to become kubernetes nodes.
	// +optional
	IgnitionEndpoint string `json:"ignitionEndpoint"`

	// Conditions represents the latest available observations of a control
	// plane's current state.
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
	Desired Release `json:"desired"`

	// history contains a list of the most recent versions applied to the cluster.
	// This value may be empty during cluster startup, and then will be updated
	// when a new update is being applied. The newest update is first in the
	// list and it is ordered by recency. Updates in the history have state
	// Completed if the rollout completed - if an update was failing or halfway
	// applied the state will be Partial. Only a limited amount of update history
	// is preserved.
	//
	// +optional
	History []configv1.UpdateHistory `json:"history,omitempty"`

	// observedGeneration reports which version of the spec is being synced.
	// If this value is not equal to metadata.generation, then the desired
	// and conditions fields may represent a previous version.
	ObservedGeneration int64 `json:"observedGeneration"`
}

// ClusterConfiguration specifies configuration for individual OCP components in the
// cluster, represented as embedded resources that correspond to the openshift
// configuration API.
//
// The API for individual configuration items is at:
// https://docs.openshift.com/container-platform/4.7/rest_api/config_apis/config-apis-index.html
type ClusterConfiguration struct {
	// SecretRefs holds references to any secrets referenced by configuration
	// entries. Entries can reference the secrets using local object references.
	//
	// +kubebuilder:validation:Optional
	// +optional
	SecretRefs []corev1.LocalObjectReference `json:"secretRefs,omitempty"`

	// ConfigMapRefs holds references to any configmaps referenced by
	// configuration entries. Entries can reference the configmaps using local
	// object references.
	//
	// +kubebuilder:validation:Optional
	// +optional
	ConfigMapRefs []corev1.LocalObjectReference `json:"configMapRefs,omitempty"`

	// Items embeds the serialized configuration resources.
	//
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
