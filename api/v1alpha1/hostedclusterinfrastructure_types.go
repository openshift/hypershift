package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Verified                 = "Verified"
	AsExpected               = "AsExpected"
	NotAsExpected            = "NotAsExpected"
	MisConfiguredReason      = "MisConfigured"
	RemovingReason           = "Removing"
	PlatfromDestroyReason    = "Destroying"
	HCIBeingConfiguredReason = "BeingConfigured"

	// ProviderSecretConfigured indicates the state of the secret reference
	ProviderSecretConfigured ConditionType = "ProviderSecretConfigured"

	// PlatformConfigured indicates (if status is true) that the
	// platform configuration specified for the platform provider has been applied
	PlatformConfigured ConditionType = "PlatformInfrastructureConfigured"

	// PlatformIAMConfigured indicates (if status is true) that the IAM is configured
	PlatformIAMConfigured ConditionType = "PlatformIAMConfigured"
)

func init() {
	SchemeBuilder.Register(&HostedClusterInfrastructure{}, &HostedClusterInfrastructureList{})
}

// HostedClusterInfrastructureSpec is the desired behavior of a HostedClusterInfrastructure.
type HostedClusterInfrastructureSpec struct {

	// InfraID is a globally unique identifier for the cluster. This identifier
	// will be used to associate various cloud resources with the HostedCluster
	// and its associated NodePools. If not specified the metadata.name for this
	// resource is used. When specified, this value is used.
	//
	// +optional
	// +immutable
	InfraID string `json:"infraID,omitempty"`

	// SubDomain is the identifier that is used between the *.apps and base-domain
	// when building an ingress URL. This will also be the name of the cluster.
	// If no value is provided, the infra-id is used
	// When SubDomain = my and base-domain = company.com, the resulting ingress URL
	// will be *.apps.my.company.com
	//
	// +optional
	// +immutable
	SubDomain string `json:"subDomain,omitempty"`

	// Platform specifies the underlying infrastructure provider for the cluster
	// and is used to configure platform specific behavior.
	//
	// +immutable
	Platform PlatformInfraSpec `json:"platform"`

	// baseDomain is the base domain of the cluster. All managed DNS records will
	// be sub-domains of this base.
	//
	// For example, given the base domain `openshift.example.com`, an API server
	// DNS record may be created for `cluster-api.openshift.example.com`.
	//
	// Once set, this field cannot be changed.
	BaseDomain string `json:"baseDomain"`

	// CloudProvider secret, contains the Cloud credential and Base Domain
	// When not present, we expect all values to populated at create time
	// This can be from the hypershift cli or via a kubectl create.
	// +optional
	CloudProvider corev1.LocalObjectReference `json:"cloudProvider,omitempty"`
}

// PlatformSpec specifies the underlying infrastructure provider for the cluster
// and is used to configure platform specific behavior.
type PlatformInfraSpec struct {
	// Type is the type of infrastructure provider for the cluster.
	//
	// +kubebuilder:validation:Enum=AWS;Azure;PowerVS
	// +immutable
	Type PlatformType `json:"type"`

	// AWS specifies configuration for clusters running on Amazon Web Services.
	//
	// +optional
	// +immutable
	AWS *AWSPlatformInfraSpec `json:"aws,omitempty"`

	// Azure defines azure specific settings
	// TODO: Add Azure type with controller capabilities
	//
	// +optional
	// +immutable
	//Azure *AzurePlatformInfraSpec `json:"azure,omitempty"`

	// PowerVS specifies configuration for clusters running on IBMCloud Power VS Service.
	// TODO: Add Azure type with controller capabilities
	// This field is immutable. Once set, It can't be changed.
	//
	// +optional
	// +immutable
	//PowerVS *PowerVSPlatformSpec `json:"powervs,omitempty"`
}

// AWSPlatformSpec specifies configuration for clusters running on Amazon Web Services.
type AWSPlatformInfraSpec struct {
	// Region is the AWS region in which the AWS infrastructure resources will be created.
	// This also used by HostedCluster and NodePools when creating a cluster
	// HostedCluster.spec.platform.aws.region
	//
	// +immutable
	Region string `json:"region"`

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

	// Zones are availability zones in an AWS region.
	//
	// +optional
	Zones []string `json:"zones,omitempty"`
}

// HostedClusterInfrastructureConfigStatus reports the IDs of the platform resources via a status field
type HostedClusterInfrastructureConfigStatus struct {

	// Platform specifies the underlying infrastructure provider for the cluster
	// and is used to configure platform specific behavior.
	//
	// +immutable
	Platform PlatformInfraConfigStatus `json:"platform"`

	// DNS specifies DNS configuration for the cluster.
	//
	// +immutable
	DNS DNSSpec `json:"dns"`

	// Networking specifies network configuration for the cluster.
	//
	// +optional
	// +immutable
	Networking ClusterNetworkingInfraConfigStatus `json:"networking,omitempty"`

	// IssuerURL is an OIDC issuer URL which is used as the issuer in all
	// ServiceAccount tokens generated by the control plane API server. The
	// default value is kubernetes.default.svc, which only works for in-cluster
	// validation.
	//
	// +kubebuilder:default:="https://kubernetes.default.svc"
	// +immutable
	// +optional
	// +kubebuilder:validation:Format=uri
	IssuerURL string `json:"issuerURL,omitempty"`
}

type ClusterNetworkingInfraConfigStatus struct {
	// MachineNetwork is the list of IP address pools for machines.
	//
	// +immutable
	MachineNetwork []MachineNetworkEntry `json:"machineNetwork,omitempty"`
}

// PlatformInfraConfigStatus specifies the underlying infrastructure provider for the cluster
// and is used to configure platform specific behavior.
type PlatformInfraConfigStatus struct {
	// AWS specifies configuration for clusters running on Amazon Web Services.
	//
	// +optional
	// +immutable
	AWS *AWSPlatformInfraConfigStatus `json:"aws,omitempty"`

	// Azure defines azure specific settings
	Azure *AzurePlatformSpec `json:"azure,omitempty"`
}

// AWSPlatformInfraConfigStatus specifies configuration for clusters running on Amazon Web Services.
type AWSPlatformInfraConfigStatus struct {

	// VPC is the VPC to use for control plane cloud resources.
	// HostedCluster.spec.platform.aws.cloudProviderConfig.vpc
	//
	// +optional
	// +immutable
	VPC string `json:"vpc,omitempty"`

	// InstanceProfile is the AWS EC2 instance profile, which is a container for an IAM role that the EC2 instance uses.
	InstanceProfile string `json:"instanceProfile,omitempty"`

	// RolesRef contains references to various AWS IAM roles required to enable
	// integrations such as OIDC.
	// HostedCluster.spec.platform.aws.rolesRef
	//
	// +optional
	// +immutable
	RolesRef *AWSRolesRef `json:"rolesRef,omitempty"`

	// SecurityGroups is an optional set of security groups to associate with node
	// instances. One of more of the security groups can be used with nodePool resources
	// NodePool.spec.platform.aws.securityGroups[]
	//
	// +optional
	SecurityGroups []AWSResourceReference `json:"securityGroups,omitempty"`

	// Zones are availability zones in an AWS region.
	// An AWS subnet is created in each zone. The info is then used to populate
	// HostedCluster.spec.platform.aws.cloudProviderConfig.zone
	// HostedCluster.spec.platform.aws.cloudProviderConfig.subnet
	// NodePool.spec.platform.aws.subnet.id
	//
	// +optional
	Zones []AWSZoneAndSubnetConfigStatus `json:"zones,omitempty"`

	// IndirectResources are created as part of the enablement for HostedCluster and NodePool(s). These indirect resources
	// are associated with other AWS resources that are directly consumed by HostedCluster and NodePool(s).
	IndirectResources AWSPlatformInfraIndirectConfigStatus `json:"indirectResources"`
}

type AWSZoneAndSubnetConfigStatus struct {
	// Subnet will be created if value is empty in the specified zone
	//
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

	// PublicSubnet will be created for each zone that a private "Subnet" is created
	//
	PublicSubnet *AWSResourceReference `json:"publicSubnet,omitempty"`

	// Zone is the availability zone to be used, a subnet will be created if one is not provided.
	// The availability zones must be a memeber of the spec.platform.aws.region
	//
	Zone string `json:"zone"`
}

// HostedClusterInfrastructure defines the observed state of HostedClusterInfrastructure
type HostedClusterInfrastructureStatus struct {
	// Track the conditions for each step in the desired curation that is being
	// executed as a job
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Infrastructure is populated with the created or discovered infrastructure resouce IDs that are
	// used when creating HostedCluster and  NodePool
	Infrastructure HostedClusterInfrastructureConfigStatus `json:"infrastructure,omitempty"`
}

// AWSPlatformInfraIndirectCosnfigStatus specifies configuration for clusters running on Amazon Web Services.
type AWSPlatformInfraIndirectConfigStatus struct {

	// DhcpOptionsID is the AWS DHCP Options resource ID, must exist and be associated with the VPC
	// +optional
	DhcpOptionsID string `json:"dhcpOptionsID"`

	// InternetGatewayID tracks the internet gateway used to communitate with the Internet,
	// must be attached to the VPC
	// +optional
	InternetGatewayID string `json:"internetGatewayID"`

	// NatGatewayID tracks the NAT gateway used for private internal communication
	// +optional
	NatGatewayID string `json:"natGatewayID"`

	// PublicEndpointRouteTableID points to the routing table for public routing
	// +optional
	PublicEndpointRouteTableID string `json:"publicEndpointRouteTableIds"`

	// PrivateEndpointRouteTableIds points to the routing tables for private routing
	// +optional
	PrivateEndpointRouteTableIds []*string `json:"privateEndpointRouteTableIds"`

	// VPCEndpointID points to an endpoint used by S3
	// +optional
	VPCEndpointID string `json:"vpcEndpointId"`

	// ElasticIpID points to an Elastic IP used by the Internet Gateway
	// +optional
	ElasticIpID string `json:"elasticIpID"`

	// LocalZoneID is the Hosted Zone ID where all the DNS records that are
	// locally accessible to the cluster exist.
	//
	// +optional
	// +immutable
	LocalZoneID string `json:"localZoneID,omitempty"`

	// OIDCProviderARN is an ARN value referencing a role appropriate for the Provider.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"iam:CreateARN"
	//			],
	//			"Resource": [
	//				"arn:aws:iam:::oidc-provider",
	//			]
	//		}
	//	]
	// }
	OIDCProviderARN string `json:"oidcProviderARN"`
}

// +genclient

// HostedClusterInfrastructure is the primary representation of a HyperShift cluster's infrastructure.
// It creates the infrastructure required for a hosted cluster. Creating a HostedClusterInfrastructure
// results in a set of provider resources that can be consumed by HostedCluster (hypershift-operator).
// This is not required for HostedCluster, but allows required infrastructure to be managed and staged,
// independent of HostClusters and NodePools
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=hostedclusterinfrastructure,shortName=hci;hcis,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TYPE",type="string",JSONPath=".spec.platform.type",description="Infrastructure type"
// +kubebuilder:printcolumn:name="INFRA",type="string",JSONPath=".status.conditions[?(@.type==\"PlatformInfrastructureConfigured\")].reason",description="Reason"
// +kubebuilder:printcolumn:name="IAM",type="string",JSONPath=".status.conditions[?(@.type==\"PlatformIAMConfigured\")].reason",description="Reason"
// +kubebuilder:printcolumn:name="PROVIDER REF",type="string",JSONPath=".status.conditions[?(@.type==\"ProviderSecretConfigured\")].reason",description="Reason"
type HostedClusterInfrastructure struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired behavior of the HostedClusterInfrastructure.
	Spec HostedClusterInfrastructureSpec `json:"spec,omitempty"`

	// Status is the latest observed status of the HostedClusterInfrastructure.
	Status HostedClusterInfrastructureStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// HostedClusterInfrastructureList contains a list of HostedClusterInfrastructure
type HostedClusterInfrastructureList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostedClusterInfrastructure `json:"items"`
}
