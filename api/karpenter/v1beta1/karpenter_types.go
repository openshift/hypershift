package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KarpenterCoreE2EOverrideAnnotation is an annotation to be applied to a HostedCluster that allows
	// overriding the default behavior of the Karpenter Operator for upstream Karpenter core E2E testing purposes.
	KarpenterCoreE2EOverrideAnnotation = "hypershift.openshift.io/karpenter-core-e2e-override"

	// KarpenterProviderAWSImage overrides the Karpenter AWS provider image to use for
	// a HostedControlPlane with AutoNode enabled.
	KarpenterProviderAWSImage = "hypershift.openshift.io/karpenter-provider-aws-image"

	// TokenSecretNodePoolAnnotation is used to annotate the Karpenter token secret with its hyperv1.NodePool namespaced name.
	TokenSecretNodePoolAnnotation = "hypershift.openshift.io/nodePool"

	// UserDataAMILabel is a label set in the userData secret generated for karpenter instances.
	UserDataAMILabel = "hypershift.openshift.io/ami"

	// ConditionTypeReady is the top-level readiness condition for the OpenshiftEC2NodeClass.
	// It is computed atomically by the EC2 node class controller, combining the upstream
	// EC2NodeClass readiness with the VersionResolved condition status.
	ConditionTypeReady = "Ready"

	// ConditionTypeVersionResolved indicates whether the spec.version was successfully resolved to a release image.
	ConditionTypeVersionResolved = "VersionResolved"

	// ConditionTypeSupportedVersionSkew signals whether the NodeClass spec.version falls within the
	// supported skew policy relative to the HostedControlPlane version.
	// NodeClass version cannot be higher than the HostedControlPlane version.
	// For 4.y versions, all versions support up to 3 minor version differences (n-3).
	// When false, the NodeClass will continue operating but there are no compatibility guarantees.
	ConditionTypeSupportedVersionSkew = "SupportedVersionSkew"

	// ConditionReasonVersionNotSpecified indicates that no spec.version was set,
	// so the NodeClass uses the control plane release image.
	ConditionReasonVersionNotSpecified = "VersionNotSpecified"

	// ConditionReasonVersionResolved indicates that spec.version was successfully
	// resolved to a release image via Cincinnati.
	ConditionReasonVersionResolved = "VersionResolved"

	// ConditionReasonResolutionFailed indicates that spec.version could not be
	// resolved to a release image.
	ConditionReasonResolutionFailed = "ResolutionFailed"

	// ConditionReasonUnsupportedSkew indicates that the NodeClass spec.version
	// falls outside the supported version skew policy relative to the control plane.
	ConditionReasonUnsupportedSkew = "UnsupportedSkew"

	// ConditionReasonAsExpected indicates that the version skew is within the
	// supported policy.
	ConditionReasonAsExpected = "AsExpected"
)

// IPAddressAssociation controls IP address assignment for instances.
// +kubebuilder:validation:Enum=Public;SubnetDefault
type IPAddressAssociation string

const (
	// IPAddressAssociationPublic assigns public IP addresses to instances, allowing direct internet access.
	IPAddressAssociationPublic IPAddressAssociation = "Public"
	// IPAddressAssociationSubnetDefault defers IP address assignment to the subnet configuration.
	IPAddressAssociationSubnetDefault IPAddressAssociation = "SubnetDefault"
)

// MonitoringState controls the monitoring level for instances.
// For more information, see Basic monitoring and detailed monitoring (https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/cloudwatch-metrics-basic-detailed.html).
// +kubebuilder:validation:Enum=Detailed;Basic
type MonitoringState string

const (
	// MonitoringStateDetailed enables detailed monitoring for instances.
	MonitoringStateDetailed MonitoringState = "Detailed"
	// MonitoringStateBasic enables basic monitoring for instances.
	MonitoringStateBasic MonitoringState = "Basic"
)

// RootVolumeDesignation indicates whether a device is mounted as the kubelet root directory.
// +kubebuilder:validation:Enum=RootVolume;NotRootVolume
type RootVolumeDesignation string

const (
	// RootVolumeDesignationRootVolume indicates the device is mounted as kubelet root dir.
	RootVolumeDesignationRootVolume RootVolumeDesignation = "RootVolume"
	// RootVolumeDesignationNotRootVolume indicates the device is not the kubelet root dir.
	RootVolumeDesignationNotRootVolume RootVolumeDesignation = "NotRootVolume"
)

// DeleteOnTerminationPolicy controls whether an EBS volume is deleted on instance termination.
// +kubebuilder:validation:Enum=Delete;Retain
type DeleteOnTerminationPolicy string

const (
	// DeleteOnTerminationPolicyDelete deletes the EBS volume when the instance terminates.
	DeleteOnTerminationPolicyDelete DeleteOnTerminationPolicy = "Delete"
	// DeleteOnTerminationPolicyRetain retains the EBS volume when the instance terminates.
	DeleteOnTerminationPolicyRetain DeleteOnTerminationPolicy = "Retain"
)

// EncryptionState controls whether an EBS volume is encrypted.
// +kubebuilder:validation:Enum=Encrypted;Unencrypted
type EncryptionState string

const (
	// EncryptionStateEncrypted indicates the EBS volume is encrypted.
	EncryptionStateEncrypted EncryptionState = "Encrypted"
	// EncryptionStateUnencrypted indicates the EBS volume is not encrypted.
	EncryptionStateUnencrypted EncryptionState = "Unencrypted"
)

// VolumeType is the type of an EBS block device.
// For more information, see Amazon EBS volume types (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/EBSVolumeTypes.html).
// +kubebuilder:validation:Enum=Standard;IO1;IO2;GP2;SC1;ST1;GP3
type VolumeType string

const (
	// VolumeTypeStandard is a previous generation magnetic volume.
	VolumeTypeStandard VolumeType = "Standard"
	// VolumeTypeIO1 is a provisioned IOPS SSD volume.
	VolumeTypeIO1 VolumeType = "IO1"
	// VolumeTypeIO2 is a provisioned IOPS SSD volume (latest generation).
	VolumeTypeIO2 VolumeType = "IO2"
	// VolumeTypeGP2 is a general purpose SSD volume.
	VolumeTypeGP2 VolumeType = "GP2"
	// VolumeTypeSC1 is a cold HDD volume.
	VolumeTypeSC1 VolumeType = "SC1"
	// VolumeTypeST1 is a throughput optimized HDD volume.
	VolumeTypeST1 VolumeType = "ST1"
	// VolumeTypeGP3 is a general purpose SSD volume (latest generation).
	VolumeTypeGP3 VolumeType = "GP3"
)

// Subnet contains resolved Subnet selector values utilized for node launch
type Subnet struct {
	// id is the ID of the subnet.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	ID string `json:"id,omitempty"`
	// zone is the associated availability zone.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Zone string `json:"zone,omitempty"`
	// zoneID is the associated availability zone ID.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	ZoneID string `json:"zoneID,omitempty"`
}

// SecurityGroup contains resolved SecurityGroup selector values utilized for node launch
type SecurityGroup struct {
	// id is the ID of the security group.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	ID string `json:"id,omitempty"`
	// name is the name of the security group.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name,omitempty"`
}

// InstanceStorePolicy enumerates options for configuring instance store disks.
// +kubebuilder:validation:Enum={RAID0}
type InstanceStorePolicy string

const (
	// InstanceStorePolicyRAID0 configures a RAID-0 array that includes all ephemeral NVMe instance storage disks.
	// The containerd and kubelet state directories (`/var/lib/containerd` and `/var/lib/kubelet`) will then use the
	// ephemeral storage for more and faster node ephemeral-storage. The node's ephemeral storage can be shared among
	// pods that request ephemeral storage and container images that are downloaded to the node.
	InstanceStorePolicyRAID0 InstanceStorePolicy = "RAID0"
)

// OpenshiftEC2NodeClassSpec defines the desired state of ClusterSizingConfiguration
// This will contain configuration necessary to launch instances in AWS.
// +kubebuilder:validation:MinProperties=1
type OpenshiftEC2NodeClassSpec struct {
	// subnetSelectorTerms is a list of or subnet selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="subnetSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id']",rule="self.all(x, has(x.tags) || has(x.id))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in subnetSelectorTerms",rule="!self.all(x, has(x.id) && has(x.tags))"
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems:=30
	// +listType=atomic
	// +optional
	SubnetSelectorTerms []SubnetSelectorTerm `json:"subnetSelectorTerms,omitempty"`

	// securityGroupSelectorTerms is a list of or security group selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="securityGroupSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id', 'name']",rule="self.all(x, has(x.tags) || has(x.id) || has(x.name))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.id) && (has(x.tags) || has(x.name)))"
	// +kubebuilder:validation:XValidation:message="'name' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.name) && (has(x.tags) || has(x.id)))"
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems:=30
	// +listType=atomic
	// +optional
	SecurityGroupSelectorTerms []SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms,omitempty"`

	// ipAddressAssociation controls the IP address assignment for instances launched with the nodeclass.
	// Valid values are:
	// - "Public": assigns public IP addresses to instances, allowing direct internet access.
	// - "SubnetDefault": defers IP address assignment to the subnet configuration.
	// When unset, the cloud provider's subnet default behavior is used.
	// +optional
	IPAddressAssociation IPAddressAssociation `json:"ipAddressAssociation,omitempty"`

	// tags to be applied on ec2 resources like instances and launch templates.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching eks:eks-cluster-name",rule="self.all(k, k !='eks:eks-cluster-name')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching kubernetes.io/cluster/",rule="self.all(k, !k.startsWith('kubernetes.io/cluster') )"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodepool",rule="self.all(k, k != 'karpenter.sh/nodepool')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodeclaim",rule="self.all(k, k !='karpenter.sh/nodeclaim')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.k8s.aws/ec2nodeclass",rule="self.all(k, k !='karpenter.k8s.aws/ec2nodeclass')"
	// +kubebuilder:validation:MinProperties=1
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// blockDeviceMappings to be applied to provisioned nodes.
	// For OpenShift nodes, a default root volume size of 120Gi and type gp3 is set if no blockDeviceMapping overrides are specified.
	// +kubebuilder:validation:XValidation:message="must have only one blockDeviceMappings with rootVolume",rule="self.filter(x, has(x.rootVolume)?x.rootVolume=='RootVolume':false).size() <= 1"
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems:=50
	// +listType=atomic
	// +optional
	BlockDeviceMappings []BlockDeviceMapping `json:"blockDeviceMappings,omitempty"`

	// instanceStorePolicy specifies how to handle instance-store disks.
	// +optional
	InstanceStorePolicy InstanceStorePolicy `json:"instanceStorePolicy,omitempty"`

	// monitoring controls the monitoring level for instances that are launched.
	// For more information, see Basic monitoring and detailed monitoring
	// (https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/cloudwatch-metrics-basic-detailed.html).
	// Valid values are:
	// - "Detailed": enables detailed monitoring.
	// - "Basic": enables basic monitoring.
	// When unset, the cloud provider's default basic monitoring behavior is used.
	// +optional
	Monitoring MonitoringState `json:"monitoring,omitempty"`

	// MetadataOptions contains parameters for specifying the exposure of the
	// Instance Metadata Service to provisioned EC2 nodes.
	// Refer to recommended, security best practices
	// (https://aws.github.io/aws-eks-best-practices/security/docs/iam/#restrict-access-to-the-instance-profile-assigned-to-the-worker-node)
	// for limiting exposure of Instance Metadata and User Data to pods.
	// If omitted, defaults to httpEndpoint enabled, with httpProtocolIPv6
	// disabled, with httpPutResponseHopLimit of 1, and with httpTokens
	// required.
	// +optional
	MetadataOptions *MetadataOptions `json:"metadataOptions,omitempty"`

	// version is an OpenShift version (e.g., "4.20.1") specifying the release version
	// for nodes managed by this NodeClass. When set, the controller resolves this to a
	// release image via the Cincinnati graph API. When not set, nodes use the control plane's
	// release image.
	// +kubebuilder:validation:XValidation:rule="self.matches('^(0|[1-9]\\\\d*)\\\\.(0|[1-9]\\\\d*)\\\\.(0|[1-9]\\\\d*)$')",message="version must be a valid semantic version (e.g., 4.20.1)"
	// +kubebuilder:validation:MinLength=5
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Version string `json:"version,omitempty"`
}

// SubnetSelectorTerm defines selection logic for a subnet used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
// +kubebuilder:validation:MinProperties=1
type SubnetSelectorTerm struct {
	// tags is a map of key/value tags used to select subnets.
	// Specifying '*' for a value selects all values for a given tag key.
	// The expected format is {"key1": "value1", "key2": "*"}.
	// +kubebuilder:validation:XValidation:message="empty tag keys or values aren't supported",rule="self.all(k, k != '' && self[k] != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +kubebuilder:validation:MinProperties=1
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// id is the subnet id in EC2.
	// The expected format is "subnet-" followed by alphanumeric characters, e.g. "subnet-0a1b2c3d4e5f".
	// +kubebuilder:validation:XValidation:rule="self.matches('^subnet-[0-9a-z]+$')",message="id must match the pattern subnet-[0-9a-z]+"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ID string `json:"id,omitempty"`
}

// SecurityGroupSelectorTerm defines selection logic for a security group used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
// +kubebuilder:validation:MinProperties=1
type SecurityGroupSelectorTerm struct {
	// tags is a map of key/value tags used to select security groups.
	// Specifying '*' for a value selects all values for a given tag key.
	// The expected format is {"key1": "value1", "key2": "*"}.
	// +kubebuilder:validation:XValidation:message="empty tag keys or values aren't supported",rule="self.all(k, k != '' && self[k] != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +kubebuilder:validation:MinProperties=1
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// id is the security group id in EC2.
	// The expected format is "sg-" followed by alphanumeric characters, e.g. "sg-0a1b2c3d4e5f".
	// +kubebuilder:validation:XValidation:rule="self.matches('^sg-[0-9a-z]+$')",message="id must match the pattern sg-[0-9a-z]+"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ID string `json:"id,omitempty"`

	// name is the security group name in EC2.
	// This value is the name field, which is different from the name tag.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Name string `json:"name,omitempty"`
}

// BlockDeviceMapping defines a block device mapping for a node.
// +kubebuilder:validation:MinProperties=1
type BlockDeviceMapping struct {
	// deviceName is the device name (for example, /dev/sdh or xvdh).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	DeviceName string `json:"deviceName,omitempty"`

	// ebs contains parameters used to automatically set up EBS volumes when an instance is launched.
	// +kubebuilder:validation:XValidation:message="snapshotID or volumeSizeGiB must be defined",rule="has(self.snapshotID) || has(self.volumeSizeGiB)"
	// +optional
	EBS BlockDevice `json:"ebs,omitempty,omitzero"`

	// rootVolume indicates whether this device is mounted as kubelet root dir. You can
	// configure at most one root volume in BlockDeviceMappings.
	// +optional
	RootVolume RootVolumeDesignation `json:"rootVolume,omitempty"`
}

// +kubebuilder:validation:MinProperties=1
type BlockDevice struct {
	// deleteOnTermination indicates whether the EBS volume is deleted on instance termination.
	// +optional
	DeleteOnTermination DeleteOnTerminationPolicy `json:"deleteOnTermination,omitempty"`

	// encrypted indicates whether the EBS volume is encrypted. Encrypted volumes can only
	// be attached to instances that support Amazon EBS encryption. If you are creating
	// a volume from a snapshot, you can't specify an encryption value.
	// +optional
	Encrypted EncryptionState `json:"encrypted,omitempty"`

	// iops is the number of I/O operations per second (IOPS). For gp3, io1, and io2 volumes,
	// this represents the number of IOPS that are provisioned for the volume. For
	// gp2 volumes, this represents the baseline performance of the volume and the
	// rate at which the volume accumulates I/O credits for bursting.
	//
	// The following are the supported values for each volume type:
	//
	//    * gp3: 3,000-16,000 IOPS
	//
	//    * io1: 100-64,000 IOPS
	//
	//    * io2: 100-64,000 IOPS
	//
	// For io1 and io2 volumes, we guarantee 64,000 IOPS only for Instances built
	// on the Nitro System (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-types.html#ec2-nitro-instances).
	// Other instance families guarantee performance up to 32,000 IOPS.
	//
	// This parameter is supported for io1, io2, and gp3 volumes only. This parameter
	// is not supported for gp2, st1, sc1, or standard volumes.
	// +optional
	IOPS *int64 `json:"iops,omitempty"`

	// kmsKeyID is the ARN of the symmetric Key Management Service (KMS) CMK used for encryption.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	KMSKeyID string `json:"kmsKeyID,omitempty"`

	// snapshotID is the ID of an EBS snapshot.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	SnapshotID string `json:"snapshotID,omitempty"`

	// throughput to provision for a gp3 volume, with a maximum of 1,000 MiB/s.
	// Valid Range: Minimum value of 125. Maximum value of 1000.
	// +optional
	Throughput *int64 `json:"throughput,omitempty"`

	// volumeSizeGiB is the size of the volume in GiB. You must specify either a snapshot ID or
	// a volume size. The following are the supported volume sizes for each volume type:
	//
	//    * gp2 and gp3: 1-16,384
	//
	//    * io1 and io2: 4-16,384
	//
	//    * st1 and sc1: 125-16,384
	//
	//    * standard: 1-1,024
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65536
	// +optional
	VolumeSizeGiB int64 `json:"volumeSizeGiB,omitempty"`

	// volumeType is the type of the block device.
	// For more information, see Amazon EBS volume types (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/EBSVolumeTypes.html)
	// in the Amazon Elastic Compute Cloud User Guide.
	// +optional
	VolumeType VolumeType `json:"volumeType,omitempty"`
}

// MetadataOptions contains parameters for specifying the exposure of the
// Instance Metadata Service to provisioned EC2 nodes.
type MetadataOptions struct {
	// HTTPEndpoint enables or disables the HTTP metadata endpoint on provisioned
	// nodes. If metadata options is non-nil, but this parameter is not specified,
	// the default state is "enabled".
	//
	// If you specify a value of "disabled", instance metadata will not be accessible
	// on the node.
	// +kubebuilder:default=enabled
	// +kubebuilder:validation:Enum:={enabled,disabled}
	// +optional
	HTTPEndpoint *string `json:"httpEndpoint,omitempty"`
	// HTTPProtocolIPv6 enables or disables the IPv6 endpoint for the instance metadata
	// service on provisioned nodes. If metadata options is non-nil, but this parameter
	// is not specified, the default state is "disabled".
	// +kubebuilder:default=disabled
	// +kubebuilder:validation:Enum:={enabled,disabled}
	// +optional
	HTTPProtocolIPv6 *string `json:"httpProtocolIPv6,omitempty"`
	// HTTPPutResponseHopLimit is the desired HTTP PUT response hop limit for
	// instance metadata requests. The larger the number, the further instance
	// metadata requests can travel. Possible values are integers from 1 to 64.
	// If metadata options is non-nil, but this parameter is not specified, the
	// default value is 1.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum:=1
	// +kubebuilder:validation:Maximum:=64
	// +optional
	HTTPPutResponseHopLimit *int64 `json:"httpPutResponseHopLimit,omitempty"`
	// HTTPTokens determines the state of token usage for instance metadata
	// requests. If metadata options is non-nil, but this parameter is not
	// specified, the default state is "required".
	//
	// If the state is optional, one can choose to retrieve instance metadata with
	// or without a signed token header on the request. If one retrieves the IAM
	// role credentials without a token, the version 1.0 role credentials are
	// returned. If one retrieves the IAM role credentials using a valid signed
	// token, the version 2.0 role credentials are returned.
	//
	// If the state is "required", one must send a signed token header with any
	// instance metadata retrieval requests. In this state, retrieving the IAM
	// role credentials always returns the version 2.0 credentials; the version
	// 1.0 credentials are not available.
	// +kubebuilder:default=required
	// +kubebuilder:validation:Enum:={required,optional}
	// +optional
	HTTPTokens *string `json:"httpTokens,omitempty"`
}

// OpenshiftEC2NodeClassStatus defines the observed state of OpenshiftEC2NodeClass.
// +kubebuilder:validation:MinProperties=1
type OpenshiftEC2NodeClassStatus struct {
	// conditions contain signals for health and readiness.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// subnets contains the current Subnet values that are available to the
	// cluster under the subnet selectors.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Subnets []Subnet `json:"subnets,omitempty"`

	// securityGroups contains the current Security Groups values that are available to the
	// cluster under the SecurityGroups selectors.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	SecurityGroups []SecurityGroup `json:"securityGroups,omitempty"`

	// releaseImage is the fully qualified release image resolved either from spec.version, or inherited from
	// the HostedControlPlane's spec.ReleaseImage.
	// Of the format "quay.io/openshift-release-dev/ocp-release@sha256:<digest>".
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	ReleaseImage string `json:"releaseImage,omitempty"`

	// version is the resolved OpenShift version corresponding to the status.releaseImage.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=openshiftec2nodeclasses,shortName=oec2nc;oec2ncs,scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// OpenshiftEC2NodeClass defines the desired state of OpenshiftEC2NodeClass.
type OpenshiftEC2NodeClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of the OpenshiftEC2NodeClass.
	// +required
	Spec OpenshiftEC2NodeClassSpec `json:"spec,omitzero"`

	// status defines the observed state of the OpenshiftEC2NodeClass.
	// +optional
	Status OpenshiftEC2NodeClassStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true
// OpenshiftEC2NodeClassList contains a list of OpenshiftEC2NodeClass.
type OpenshiftEC2NodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenshiftEC2NodeClass `json:"items"`
}
