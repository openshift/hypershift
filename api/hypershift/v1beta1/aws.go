package v1beta1

// AWSNodePoolPlatform specifies the configuration of a NodePool when operating
// on AWS.
type AWSNodePoolPlatform struct {
	// instanceType is an ec2 instance type for node instances (e.g. m5.large).
	// +required
	// +kubebuilder:validation:MaxLength=255
	InstanceType string `json:"instanceType"`

	// instanceProfile is the AWS EC2 instance profile, which is a container for an IAM role that the EC2 instance uses.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	InstanceProfile string `json:"instanceProfile,omitempty"`

	// subnet is the subnet to use for node instances.
	// +kubebuilder:validation:XValidation:rule="has(self.id) && self.id.startsWith('subnet-') ? !has(self.filters) : size(self.filters) > 0", message="subnet is invalid, a valid subnet id or filters must be set, but not both"
	// +required
	Subnet AWSResourceReference `json:"subnet"`

	// ami is the image id to use for node instances. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	AMI string `json:"ami,omitempty"`

	// imageType specifies the type of image to use for node instances.
	// Valid values are "Linux" or "Windows"
	// When set to ImageTypeWindows, the controller will automatically populate the AMI field
	// with a Windows-compatible AMI based on the region and OpenShift version.
	// When the AMI field is also set, it will take precedence over automatic selection based
	// on imageType. Also note that Windows ImageType is only compatible with an Arch of amd64
	//
	// +optional
	// +kubebuilder:validation:Enum=Windows;Linux
	ImageType ImageType `json:"imageType,omitempty"`

	// securityGroups is an optional set of security groups to associate with node
	// instances.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=50
	SecurityGroups []AWSResourceReference `json:"securityGroups,omitempty"`

	// rootVolume specifies configuration for the root volume of node instances.
	//
	// +optional
	RootVolume *Volume `json:"rootVolume,omitempty"`

	// resourceTags is an optional list of additional tags to apply to AWS node
	// instances. Changes to this field will be propagated in-place to AWS EC2 instances and their initial EBS volumes.
	// Volumes created by the storage operator and attached to instances after they are created do not get these tags applied.
	//
	// These will be merged with HostedCluster scoped tags, which take precedence in case of conflicts.
	// These take precedence over tags defined out of band (i.e., tags added manually or by other tools outside of HyperShift) in AWS in case of conflicts.
	//
	// See https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for
	// information on tagging AWS resources. AWS supports a maximum of 50 tags per
	// resource. OpenShift reserves 25 tags for its use, leaving 25 tags available
	// for the user.
	//
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// placement specifies the placement options for the EC2 instances.
	//
	// +optional
	Placement *PlacementOptions `json:"placement,omitempty"`
}

// PlacementOptions specifies the placement options for the EC2 instances.
// +kubebuilder:validation:XValidation:rule="has(self.tenancy) && self.tenancy == 'host' ? !has(self.capacityReservation) : true", message="AWS Capacity Reservations cannot be used with Dedicated Hosts (tenancy 'host')"
type PlacementOptions struct {
	// tenancy indicates if instance should run on shared or single-tenant hardware.
	//
	// Possible values:
	// - "default": NodePool instances run on shared hardware.
	// - "dedicated": Each NodePool instance runs on single-tenant hardware (Dedicated Instances).
	// - "host": NodePool instances run on user's pre-allocated dedicated hosts (Dedicated Hosts).
	//
	// When tenancy is set to "host", capacityReservation cannot be specified
	// as AWS does not support Capacity Reservations with Dedicated Hosts.
	//
	// +optional
	// +kubebuilder:validation:Enum:=default;dedicated;host
	Tenancy string `json:"tenancy,omitempty"`

	// capacityReservation specifies Capacity Reservation options for the NodePool instances.
	//
	// Cannot be specified when tenancy is set to "host" as Dedicated Hosts
	// do not support Capacity Reservations. Compatible with "default" and "dedicated" tenancy.
	//
	// +optional
	CapacityReservation *CapacityReservationOptions `json:"capacityReservation,omitempty"`
}

// MarketType describes the market type of the CapacityReservation for an Instance.
type MarketType string

const (
	// MarketTypeOnDemand is a MarketType enum value
	MarketTypeOnDemand MarketType = "OnDemand"

	// MarketTypeCapacityBlock is a MarketType enum value
	MarketTypeCapacityBlock MarketType = "CapacityBlocks"
)

// CapacityReservationOptions specifies Capacity Reservation options for the NodePool instances.
// +kubebuilder:validation:XValidation:rule="has(self.id) ? (!has(self.preference) || !(self.preference in ['Open','None'])) : true", message="AWS Capacity Reservation preference 'None' or 'Open' is incompatible with specifying a Capacity Reservation ID"
// +kubebuilder:validation:XValidation:rule="has(self.marketType) && self.marketType == 'CapacityBlocks' ? has(self.id) : true", message="AWS Capacity Reservation market type 'CapacityBlocks' requires a Capacity Reservation ID"
type CapacityReservationOptions struct {
	// id specifies the target Capacity Reservation into which the EC2 instances should be launched.
	// Must follow the format: cr- followed by 17 lowercase hexadecimal characters. For example: cr-0123456789abcdef0
	// When empty, no specific Capacity Reservation is targeted.
	//
	// When specified, preference cannot be set to 'None' or 'Open' as these
	// are mutually exclusive with targeting a specific reservation. Use preference 'CapacityReservationsOnly'
	// or omit preference field when targeting a specific reservation.
	//
	// +kubebuilder:validation:XValidation:rule="self.matches('^cr-[a-f0-9]{17}$')", message="AWS Capacity Reservation ID must start with 'cr-' followed by 17 lowercase hexadecimal characters (e.g., cr-0123456789abcdef0)"
	// +kubebuilder:validation:MaxLength=20
	// +optional
	ID *string `json:"id,omitempty"`

	// marketType specifies the market type of the CapacityReservation for the EC2 instances. Valid values are OnDemand, CapacityBlocks and omitted:
	// - "OnDemand": EC2 instances run as standard On-Demand instances.
	// - "CapacityBlocks": scheduled pre-purchased compute capacity. Capacity Blocks is recommended when GPUs are needed to support ML workloads.
	// When omitted, this means no opinion and the platform is left to choose a reasonable default, which is subject to change over time.
	// The current default value is CapacityBlocks.
	//
	// When set to 'CapacityBlocks', a specific Capacity Reservation ID must be provided.
	//
	// +kubebuilder:validation:Enum:=OnDemand;CapacityBlocks
	// +optional
	MarketType MarketType `json:"marketType,omitempty"`

	// preference specifies the preference for use of Capacity Reservations by the instance. Valid values include:
	// - "": No preference (platform default)
	// - "Open": The instance may make use of open Capacity Reservations that match its AZ and InstanceType
	// - "None": The instance may not make use of any Capacity Reservations. This is to conserve open reservations for desired workloads
	// - "CapacityReservationsOnly": The instance will only run if matched or targeted to a Capacity Reservation
	//
	// Cannot be set to 'None' or 'Open' when a specific Capacity Reservation ID is provided,
	// as targeting a specific reservation is mutually exclusive with these general preference settings.
	//
	// +kubebuilder:validation:Enum="";None;CapacityReservationsOnly;Open
	// +optional
	Preference CapacityReservationPreference `json:"preference,omitempty"`
}

// CapacityReservationPreference describes the preferred use of capacity reservations
// of an instance
type CapacityReservationPreference string

const (
	// CapacityReservationPreferenceNone the instance may not make use of any Capacity Reservations. This is to conserve open reservations for desired workloads
	CapacityReservationPreferenceNone CapacityReservationPreference = "None"

	// CapacityReservationPreferenceOnly the instance will only run if matched or targeted to a Capacity Reservation
	CapacityReservationPreferenceOnly CapacityReservationPreference = "CapacityReservationsOnly"

	// CapacityReservationPreferenceOpen the instance may make use of open Capacity Reservations that match its AZ and InstanceType.
	CapacityReservationPreferenceOpen CapacityReservationPreference = "Open"
)

// AWSResourceReference is a reference to a specific AWS resource by ID or filters.
// Only one of ID or Filters may be specified. Specifying more than one will result in
// a validation error.
type AWSResourceReference struct {
	// id of resource
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ID *string `json:"id,omitempty"`

	// filters is a set of key/value pairs used to identify a resource
	// They are applied according to the rules defined by the AWS API:
	// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Filtering.html
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Filters []Filter `json:"filters,omitempty"`
}

// Filter is a filter used to identify an AWS resource
type Filter struct {
	// name is the name of the filter.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// values is a list of values for the filter.
	// +required
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=255
	Values []string `json:"values"`
}

// Volume specifies the configuration options for node instance storage devices.
type Volume struct {
	// size is the size of the volume in gibibytes (GiB).
	//
	// Must be greater than the image snapshot size or 8 (whichever is greater).
	//
	// +kubebuilder:validation:Minimum=8
	// +required
	Size int64 `json:"size"`

	// type is the type of volume to provision.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Type string `json:"type"`

	// iops is the number of IOPS requested for the disk. This is only valid
	// for type io1.
	//
	// +optional
	IOPS int64 `json:"iops,omitempty"`

	// encrypted indicates whether the EBS volume should be encrypted or not.
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`

	// encryptionKey is the KMS key to use for volume encryption.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	EncryptionKey string `json:"encryptionKey,omitempty"`
}

// AWSCloudProviderConfig specifies AWS networking configuration.
type AWSCloudProviderConfig struct {
	// subnet is the subnet to use for control plane cloud resources.
	//
	// +optional
	Subnet *AWSResourceReference `json:"subnet,omitempty"`

	// zone is the availability zone where control plane cloud resources are
	// created.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Zone string `json:"zone,omitempty"`

	// vpc is the VPC to use for control plane cloud resources.
	// +required
	// +kubebuilder:validation:MaxLength=255
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
	// region is the AWS region in which the cluster resides. This configures the
	// OCP control plane cloud integrations, and is used by NodePool to resolve
	// the correct boot AMI for a given release.
	//
	// +immutable
	// +required
	// +kubebuilder:validation:MaxLength=255
	Region string `json:"region"`

	// cloudProviderConfig specifies AWS networking configuration for the control
	// plane.
	// This is mainly used for cloud provider controller config:
	// https://github.com/kubernetes/kubernetes/blob/f5be5052e3d0808abb904aebd3218fe4a5c2dd82/staging/src/k8s.io/legacy-cloud-providers/aws/aws.go#L1347-L1364
	// TODO(dan): should this be named AWSNetworkConfig?
	//
	// +optional
	// +immutable
	CloudProviderConfig *AWSCloudProviderConfig `json:"cloudProviderConfig,omitempty"`

	// serviceEndpoints specifies optional custom endpoints which will override
	// the default service endpoint of specific AWS Services.
	//
	// There must be only one ServiceEndpoint for a given service name.
	//
	// +optional
	// +immutable
	// +kubebuilder:validation:MaxItems=50
	ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`

	// rolesRef contains references to various AWS IAM roles required to enable
	// integrations such as OIDC.
	//
	// +immutable
	// +required
	RolesRef AWSRolesRef `json:"rolesRef"`

	// resourceTags is a list of additional tags to apply to AWS resources created
	// for the cluster. See
	// https://docs.aws.amazon.com/general/latest/gr/aws_tagging.html for
	// information on tagging AWS resources. AWS supports a maximum of 50 tags per
	// resource. OpenShift reserves 25 tags for its use, leaving 25 tags available
	// for the user.
	// Changes to this field will be propagated in-place to AWS resources (VPC Endpoints, EC2 instances, initial EBS volumes and default/endpoint security groups).
	// These tags will be propagated to the infrastructure CR in the guest cluster, where other OCP operators might choose to honor this input to reconcile AWS resources created by them.
	// Please consult the official documentation for a list of all AWS resources that support in-place tag updates.
	// These take precedence over tags defined out of band (i.e., tags added manually or by other tools outside of HyperShift) in AWS in case of conflicts.
	//
	// +kubebuilder:validation:MaxItems=25
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`

	// endpointAccess specifies the publishing scope of cluster endpoints. The
	// default is Public.
	//
	// +kubebuilder:validation:Enum=Public;PublicAndPrivate;Private
	// +kubebuilder:default=Public
	// +optional
	EndpointAccess AWSEndpointAccessType `json:"endpointAccess,omitempty"`

	// additionalAllowedPrincipals specifies a list of additional allowed principal ARNs
	// to be added to the hosted control plane's VPC Endpoint Service to enable additional
	// VPC Endpoint connection requests to be automatically accepted.
	// See https://docs.aws.amazon.com/vpc/latest/privatelink/configure-endpoint-service.html
	// for more details around VPC Endpoint Service allowed principals.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=25
	// +kubebuilder:validation:items:MaxLength=255
	AdditionalAllowedPrincipals []string `json:"additionalAllowedPrincipals,omitempty"`

	// multiArch specifies whether the Hosted Cluster will be expected to support NodePools with different
	// CPU architectures, i.e., supporting arm64 NodePools and supporting amd64 NodePools on the same Hosted Cluster.
	// Deprecated: This field is no longer used. The HyperShift Operator now performs multi-arch validations
	// automatically despite the platform type. The HyperShift Operator will set HostedCluster.Status.PayloadArch based
	// on the HostedCluster release image. This field is used by the NodePool controller to validate the
	// NodePool.Spec.Arch is supported.
	// +kubebuilder:default=false
	// +optional
	MultiArch bool `json:"multiArch"`

	// sharedVPC contains fields that must be specified if the HostedCluster must use a VPC that is
	// created in a different AWS account and is shared with the AWS account where the HostedCluster
	// will be created.
	//
	// +optional
	SharedVPC *AWSSharedVPC `json:"sharedVPC,omitempty"`
}

// AWSSharedVPC contains fields needed to create a HostedCluster using a VPC that has been
// created and shared from a different AWS account than the AWS account where the cluster
// is getting created.
type AWSSharedVPC struct {
	// rolesRef contains references to roles in the VPC owner account that enable a
	// HostedCluster on a shared VPC.
	//
	// +required
	RolesRef AWSSharedVPCRolesRef `json:"rolesRef"`

	// localZoneID is the ID of the route53 hosted zone for [cluster-name].hypershift.local that is
	// associated with the HostedCluster's VPC and exists in the VPC owner account.
	//
	// +required
	// +kubebuilder:validation:MaxLength=32
	LocalZoneID string `json:"localZoneID"`
}

type AWSRoleCredentials struct {
	// arn is the ARN of the role.
	// +required
	// +kubebuilder:validation:MaxLength=2048
	ARN string `json:"arn"`
	// namespace is the namespace of the role.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Namespace string `json:"namespace"`
	// name is the name of the role.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`
}

// AWSResourceTag is a tag to apply to AWS resources created for the cluster.
type AWSResourceTag struct {
	// key is the key of the tag.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	Key string `json:"key"`
	// value is the value of the tag.
	//
	// Some AWS service do not support empty values. Since tags are added to
	// resources in many services, the length of the tag value must meet the
	// requirements of all services.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^[0-9A-Za-z_.:/=+-@]+$`
	Value string `json:"value"`
}

// AWSRolesRef contains references to various AWS IAM roles required for operators to make calls against the AWS API.
// The referenced role must have a trust relationship that allows it to be assumed via web identity.
// https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_oidc.html.
// Example:
//
//	{
//			"Version": "2012-10-17",
//			"Statement": [
//				{
//					"Effect": "Allow",
//					"Principal": {
//						"Federated": "{{ .ProviderARN }}"
//					},
//						"Action": "sts:AssumeRoleWithWebIdentity",
//					"Condition": {
//						"StringEquals": {
//							"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
//						}
//					}
//				}
//			]
//		}
type AWSRolesRef struct {
	// ingressARN is an ARN value referencing a role appropriate for the Ingress Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"elasticloadbalancing:DescribeLoadBalancers",
	//				"tag:GetResources",
	//				"route53:ListHostedZones"
	//			],
	//			"Resource": "*"
	//		},
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"route53:ChangeResourceRecordSets"
	//			],
	//			"Resource": [
	//				"arn:aws:route53:::PUBLIC_ZONE_ID",
	//				"arn:aws:route53:::PRIVATE_ZONE_ID"
	//			]
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	IngressARN string `json:"ingressARN"`

	// imageRegistryARN is an ARN value referencing a role appropriate for the Image Registry Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"s3:CreateBucket",
	//				"s3:DeleteBucket",
	//				"s3:PutBucketTagging",
	//				"s3:GetBucketTagging",
	//				"s3:PutBucketPublicAccessBlock",
	//				"s3:GetBucketPublicAccessBlock",
	//				"s3:PutEncryptionConfiguration",
	//				"s3:GetEncryptionConfiguration",
	//				"s3:PutLifecycleConfiguration",
	//				"s3:GetLifecycleConfiguration",
	//				"s3:GetBucketLocation",
	//				"s3:ListBucket",
	//				"s3:GetObject",
	//				"s3:PutObject",
	//				"s3:DeleteObject",
	//				"s3:ListBucketMultipartUploads",
	//				"s3:AbortMultipartUpload",
	//				"s3:ListMultipartUploadParts"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	ImageRegistryARN string `json:"imageRegistryARN"`

	// storageARN is an ARN value referencing a role appropriate for the Storage Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:AttachVolume",
	//				"ec2:CreateSnapshot",
	//				"ec2:CreateTags",
	//				"ec2:CreateVolume",
	//				"ec2:DeleteSnapshot",
	//				"ec2:DeleteTags",
	//				"ec2:DeleteVolume",
	//				"ec2:DescribeInstances",
	//				"ec2:DescribeSnapshots",
	//				"ec2:DescribeTags",
	//				"ec2:DescribeVolumes",
	//				"ec2:DescribeVolumesModifications",
	//				"ec2:DetachVolume",
	//				"ec2:ModifyVolume"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	StorageARN string `json:"storageARN"`

	// networkARN is an ARN value referencing a role appropriate for the Network Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:DescribeInstances",
	//        "ec2:DescribeInstanceStatus",
	//        "ec2:DescribeInstanceTypes",
	//        "ec2:UnassignPrivateIpAddresses",
	//        "ec2:AssignPrivateIpAddresses",
	//        "ec2:UnassignIpv6Addresses",
	//        "ec2:AssignIpv6Addresses",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeNetworkInterfaces"
	//			],
	//			"Resource": "*"
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	NetworkARN string `json:"networkARN"`

	// kubeCloudControllerARN is an ARN value referencing a role appropriate for the KCM/KCC.
	// Source: https://cloud-provider-aws.sigs.k8s.io/prerequisites/#iam-policies
	//
	// The following is an example of a valid policy document:
	//
	//  {
	//  "Version": "2012-10-17",
	//  "Statement": [
	//    {
	//      "Action": [
	//        "autoscaling:DescribeAutoScalingGroups",
	//        "autoscaling:DescribeLaunchConfigurations",
	//        "autoscaling:DescribeTags",
	//        "ec2:DescribeAvailabilityZones",
	//        "ec2:DescribeInstances",
	//        "ec2:DescribeImages",
	//        "ec2:DescribeRegions",
	//        "ec2:DescribeRouteTables",
	//        "ec2:DescribeSecurityGroups",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeVolumes",
	//        "ec2:CreateSecurityGroup",
	//        "ec2:CreateTags",
	//        "ec2:CreateVolume",
	//        "ec2:ModifyInstanceAttribute",
	//        "ec2:ModifyVolume",
	//        "ec2:AttachVolume",
	//        "ec2:AuthorizeSecurityGroupIngress",
	//        "ec2:CreateRoute",
	//        "ec2:DeleteRoute",
	//        "ec2:DeleteSecurityGroup",
	//        "ec2:DeleteVolume",
	//        "ec2:DetachVolume",
	//        "ec2:RevokeSecurityGroupIngress",
	//        "ec2:DescribeVpcs",
	//        "elasticloadbalancing:AddTags",
	//        "elasticloadbalancing:AttachLoadBalancerToSubnets",
	//        "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
	//        "elasticloadbalancing:CreateLoadBalancer",
	//        "elasticloadbalancing:CreateLoadBalancerPolicy",
	//        "elasticloadbalancing:CreateLoadBalancerListeners",
	//        "elasticloadbalancing:ConfigureHealthCheck",
	//        "elasticloadbalancing:DeleteLoadBalancer",
	//        "elasticloadbalancing:DeleteLoadBalancerListeners",
	//        "elasticloadbalancing:DescribeLoadBalancers",
	//        "elasticloadbalancing:DescribeLoadBalancerAttributes",
	//        "elasticloadbalancing:DetachLoadBalancerFromSubnets",
	//        "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
	//        "elasticloadbalancing:ModifyLoadBalancerAttributes",
	//        "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
	//        "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
	//        "elasticloadbalancing:AddTags",
	//        "elasticloadbalancing:CreateListener",
	//        "elasticloadbalancing:CreateTargetGroup",
	//        "elasticloadbalancing:DeleteListener",
	//        "elasticloadbalancing:DeleteTargetGroup",
	//        "elasticloadbalancing:DeregisterTargets",
	//        "elasticloadbalancing:DescribeListeners",
	//        "elasticloadbalancing:DescribeLoadBalancerPolicies",
	//        "elasticloadbalancing:DescribeTargetGroups",
	//        "elasticloadbalancing:DescribeTargetHealth",
	//        "elasticloadbalancing:ModifyListener",
	//        "elasticloadbalancing:ModifyTargetGroup",
	//        "elasticloadbalancing:RegisterTargets",
	//        "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
	//        "iam:CreateServiceLinkedRole",
	//        "kms:DescribeKey"
	//      ],
	//      "Resource": [
	//        "*"
	//      ],
	//      "Effect": "Allow"
	//    }
	//  ]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	KubeCloudControllerARN string `json:"kubeCloudControllerARN"`

	// nodePoolManagementARN is an ARN value referencing a role appropriate for the CAPI Controller.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//   "Version": "2012-10-17",
	//  "Statement": [
	//    {
	//      "Action": [
	//        "ec2:AssociateRouteTable",
	//        "ec2:AttachInternetGateway",
	//        "ec2:AuthorizeSecurityGroupIngress",
	//        "ec2:CreateInternetGateway",
	//        "ec2:CreateNatGateway",
	//        "ec2:CreateRoute",
	//        "ec2:CreateRouteTable",
	//        "ec2:CreateSecurityGroup",
	//        "ec2:CreateSubnet",
	//        "ec2:CreateTags",
	//        "ec2:DeleteInternetGateway",
	//        "ec2:DeleteNatGateway",
	//        "ec2:DeleteRouteTable",
	//        "ec2:DeleteSecurityGroup",
	//        "ec2:DeleteSubnet",
	//        "ec2:DeleteTags",
	//        "ec2:DescribeAccountAttributes",
	//        "ec2:DescribeAddresses",
	//        "ec2:DescribeAvailabilityZones",
	//        "ec2:DescribeImages",
	//        "ec2:DescribeInstances",
	//        "ec2:DescribeInternetGateways",
	//        "ec2:DescribeNatGateways",
	//        "ec2:DescribeNetworkInterfaces",
	//        "ec2:DescribeNetworkInterfaceAttribute",
	//        "ec2:DescribeRouteTables",
	//        "ec2:DescribeSecurityGroups",
	//        "ec2:DescribeSubnets",
	//        "ec2:DescribeVpcs",
	//        "ec2:DescribeVpcAttribute",
	//        "ec2:DescribeVolumes",
	//        "ec2:DetachInternetGateway",
	//        "ec2:DisassociateRouteTable",
	//        "ec2:DisassociateAddress",
	//        "ec2:ModifyInstanceAttribute",
	//        "ec2:ModifyNetworkInterfaceAttribute",
	//        "ec2:ModifySubnetAttribute",
	//        "ec2:RevokeSecurityGroupIngress",
	//        "ec2:RunInstances",
	//        "ec2:TerminateInstances",
	//        "tag:GetResources",
	//        "ec2:CreateLaunchTemplate",
	//        "ec2:CreateLaunchTemplateVersion",
	//        "ec2:DescribeLaunchTemplates",
	//        "ec2:DescribeLaunchTemplateVersions",
	//        "ec2:DeleteLaunchTemplate",
	//        "ec2:DeleteLaunchTemplateVersions"
	//      ],
	//      "Resource": [
	//        "*"
	//      ],
	//      "Effect": "Allow"
	//    },
	//    {
	//      "Condition": {
	//        "StringLike": {
	//          "iam:AWSServiceName": "elasticloadbalancing.amazonaws.com"
	//        }
	//      },
	//      "Action": [
	//        "iam:CreateServiceLinkedRole"
	//      ],
	//      "Resource": [
	//        "arn:*:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing"
	//      ],
	//      "Effect": "Allow"
	//    },
	//    {
	//      "Action": [
	//        "iam:PassRole"
	//      ],
	//      "Resource": [
	//        "arn:*:iam::*:role/*-worker-role"
	//      ],
	//      "Effect": "Allow"
	//    },
	// 	  {
	// 	  	"Effect": "Allow",
	// 	  	"Action": [
	// 	  		"kms:Decrypt",
	// 	  		"kms:ReEncrypt",
	// 	  		"kms:GenerateDataKeyWithoutPlainText",
	// 	  		"kms:DescribeKey"
	// 	  	],
	// 	  	"Resource": "*"
	// 	  },
	// 	  {
	// 	  	"Effect": "Allow",
	// 	  	"Action": [
	// 	  		"kms:CreateGrant"
	// 	  	],
	// 	  	"Resource": "*",
	// 	  	"Condition": {
	// 	  		"Bool": {
	// 	  			"kms:GrantIsForAWSResource": true
	// 	  		}
	// 	  	}
	// 	  }
	//  ]
	// }
	//
	// +required
	// +kubebuilder:validation:MaxLength=2048
	NodePoolManagementARN string `json:"nodePoolManagementARN"`

	// controlPlaneOperatorARN  is an ARN value referencing a role appropriate for the Control Plane Operator.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"ec2:CreateVpcEndpoint",
	//				"ec2:DescribeVpcEndpoints",
	//				"ec2:ModifyVpcEndpoint",
	//				"ec2:DeleteVpcEndpoints",
	//				"ec2:CreateTags",
	//				"route53:ListHostedZones",
	//				"ec2:CreateSecurityGroup",
	//				"ec2:AuthorizeSecurityGroupIngress",
	//				"ec2:AuthorizeSecurityGroupEgress",
	//				"ec2:DeleteSecurityGroup",
	//				"ec2:RevokeSecurityGroupIngress",
	//				"ec2:RevokeSecurityGroupEgress",
	//				"ec2:DescribeSecurityGroups",
	//				"ec2:DescribeVpcs",
	//			],
	//			"Resource": "*"
	//		},
	//		{
	//			"Effect": "Allow",
	//			"Action": [
	//				"route53:ChangeResourceRecordSets",
	//				"route53:ListResourceRecordSets"
	//			],
	//			"Resource": "arn:aws:route53:::%s"
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	ControlPlaneOperatorARN string `json:"controlPlaneOperatorARN"`
}

// AWSSharedVPCRolesRef contains references to AWS IAM roles required for a shared VPC hosted cluster.
// These roles must exist in the VPC owner's account.
type AWSSharedVPCRolesRef struct {
	// ingressARN is an ARN value referencing the role in the VPC owner account that allows the
	// ingress operator in the cluster account to create and manage records in the private DNS
	// hosted zone.
	//
	// The referenced role must have a trust relationship that allows it to be assumed by the
	// ingress operator role in the VPC creator account.
	// Example:
	// {
	// 	 "Version": "2012-10-17",
	// 	 "Statement": [
	// 	 	{
	// 	 		"Sid": "Statement1",
	// 	 		"Effect": "Allow",
	// 	 		"Principal": {
	// 	 			"AWS": "arn:aws:iam::[cluster-creator-account-id]:role/[infra-id]-openshift-ingress"
	// 	 		},
	// 	 		"Action": "sts:AssumeRole"
	// 	 	}
	// 	 ]
	// }
	//
	// The following is an example of the policy document for this role.
	// (Based on https://docs.openshift.com/rosa/rosa_install_access_delete_clusters/rosa-shared-vpc-config.html#rosa-sharing-vpc-dns-and-roles_rosa-shared-vpc-config)
	//
	// {
	// 	"Version": "2012-10-17",
	// 	"Statement": [
	// 		{
	// 			"Effect": "Allow",
	// 			"Action": [
	// 				"route53:ListHostedZones",
	// 				"route53:ListHostedZonesByName",
	// 				"route53:ChangeTagsForResource",
	// 				"route53:GetAccountLimit",
	// 				"route53:GetChange",
	// 				"route53:GetHostedZone",
	// 				"route53:ListTagsForResource",
	// 				"route53:UpdateHostedZoneComment",
	// 				"tag:GetResources",
	// 				"tag:UntagResources"
	// 				"route53:ChangeResourceRecordSets",
	// 				"route53:ListResourceRecordSets"
	// 			],
	// 			"Resource": "*"
	// 		},
	// 	]
	// }
	//
	// +required
	// +kubebuilder:validation:Pattern:=`^arn:(aws|aws-cn|aws-us-gov):iam::[0-9]{12}:role\/.*$`
	// +kubebuilder:validation:MaxLength=2048
	IngressARN string `json:"ingressARN"`

	// controlPlaneARN is an ARN value referencing the role in the VPC owner account that allows
	// the control plane operator in the cluster account to create and manage a VPC endpoint, its
	// corresponding Security Group, and DNS records in the hypershift local hosted zone.
	//
	// The referenced role must have a trust relationship that allows it to be assumed by the
	// control plane operator role in the VPC creator account.
	// Example:
	// {
	// 	 "Version": "2012-10-17",
	// 	 "Statement": [
	// 	 	{
	// 	 		"Sid": "Statement1",
	// 	 		"Effect": "Allow",
	// 	 		"Principal": {
	// 	 			"AWS": "arn:aws:iam::[cluster-creator-account-id]:role/[infra-id]-control-plane-operator"
	// 	 		},
	// 	 		"Action": "sts:AssumeRole"
	// 	 	}
	// 	 ]
	// }
	//
	// The following is an example of the policy document for this role.
	//
	// {
	// 	"Version": "2012-10-17",
	// 	"Statement": [
	// 		{
	// 			"Effect": "Allow",
	// 			"Action": [
	// 				"ec2:CreateVpcEndpoint",
	// 				"ec2:DescribeVpcEndpoints",
	// 				"ec2:ModifyVpcEndpoint",
	// 				"ec2:DeleteVpcEndpoints",
	// 				"ec2:CreateTags",
	// 				"route53:ListHostedZones",
	// 				"ec2:CreateSecurityGroup",
	// 				"ec2:AuthorizeSecurityGroupIngress",
	// 				"ec2:AuthorizeSecurityGroupEgress",
	// 				"ec2:DeleteSecurityGroup",
	// 				"ec2:RevokeSecurityGroupIngress",
	// 				"ec2:RevokeSecurityGroupEgress",
	// 				"ec2:DescribeSecurityGroups",
	// 				"ec2:DescribeVpcs",
	// 				"route53:ChangeResourceRecordSets",
	// 				"route53:ListResourceRecordSets"
	// 			],
	// 			"Resource": "*"
	// 		}
	// 	]
	// }
	//
	// +required
	// +kubebuilder:validation:Pattern:=`^arn:(aws|aws-cn|aws-us-gov):iam::[0-9]{12}:role\/.*$`
	// +kubebuilder:validation:MaxLength=2048
	ControlPlaneARN string `json:"controlPlaneARN"`
}

// AWSServiceEndpoint stores the configuration for services to
// override existing defaults of AWS Services.
type AWSServiceEndpoint struct {
	// name is the name of the AWS service.
	// This must be provided and cannot be empty.
	// +required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// url is fully qualified URI with scheme https, that overrides the default generated
	// endpoint for a client.
	// This must be provided and cannot be empty.
	//
	// +required
	// +kubebuilder:validation:Pattern=`^https://`
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url"`
}

// AWSKMSSpec defines metadata about the configuration of the AWS KMS Secret Encryption provider
type AWSKMSSpec struct {
	// region contains the AWS region
	// +required
	// +kubebuilder:validation:MaxLength=255
	Region string `json:"region"`
	// activeKey defines the active key used to encrypt new secrets
	// +required
	ActiveKey AWSKMSKeyEntry `json:"activeKey"`
	// backupKey defines the old key during the rotation process so previously created
	// secrets can continue to be decrypted until they are all re-encrypted with the active key.
	// +optional
	BackupKey *AWSKMSKeyEntry `json:"backupKey,omitempty"`
	// auth defines metadata about the management of credentials used to interact with AWS KMS
	// +required
	Auth AWSKMSAuthSpec `json:"auth"`
}

// AWSKMSAuthSpec defines metadata about the management of credentials used to interact and encrypt data via AWS KMS key.
// The referenced role must have a trust relationship that allows it to be assumed via web identity.
// https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_oidc.html.
// Example:
//
//	{
//			"Version": "2012-10-17",
//			"Statement": [
//				{
//					"Effect": "Allow",
//					"Principal": {
//						"Federated": "{{ .ProviderARN }}"
//					},
//						"Action": "sts:AssumeRoleWithWebIdentity",
//					"Condition": {
//						"StringEquals": {
//							"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
//						}
//					}
//				}
//			]
//		}
type AWSKMSAuthSpec struct {
	// awsKms is an ARN value referencing a role appropriate for managing the auth via the AWS KMS key.
	//
	// The following is an example of a valid policy document:
	//
	// {
	//	"Version": "2012-10-17",
	//	"Statement": [
	//    	{
	//			"Effect": "Allow",
	//			"Action": [
	//				"kms:Encrypt",
	//				"kms:Decrypt",
	//				"kms:ReEncrypt*",
	//				"kms:GenerateDataKey*",
	//				"kms:DescribeKey"
	//			],
	//			"Resource": %q
	//		}
	//	]
	// }
	// +required
	// +kubebuilder:validation:MaxLength=2048
	AWSKMSRoleARN string `json:"awsKms"`
}

// AWSKMSKeyEntry defines metadata to locate the encryption key in AWS
type AWSKMSKeyEntry struct {
	// arn is the Amazon Resource Name for the encryption key
	// +required
	// +kubebuilder:validation:Pattern=`^arn:`
	// +kubebuilder:validation:MaxLength=2048
	ARN string `json:"arn"`
}

// AWSPlatformStatus contains status specific to the AWS platform
type AWSPlatformStatus struct {
	// defaultWorkerSecurityGroupID is the ID of a security group created by
	// the control plane operator. It is always added to worker machines in
	// addition to any security groups specified in the NodePool.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	DefaultWorkerSecurityGroupID string `json:"defaultWorkerSecurityGroupID,omitempty"`
}
