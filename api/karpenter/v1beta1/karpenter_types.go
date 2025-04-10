package v1beta1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KarpenterCoreE2EOverrideAnnotation is an annotation to be applied to a HostedCluster that allows
	// overriding the default behavior of the Karpenter Operator for upstream Karpenter core E2E testing purposes.
	KarpenterCoreE2EOverrideAnnotation = "hypershift.openshift.io/karpenter-core-e2e-override"

	// KarpenterProviderAWSImage overrides the Karpenter AWS provider image to use for
	// a HostedControlPlane with AutoNode enabled.
	KarpenterProviderAWSImage = "hypershift.openshift.io/karpenter-provider-aws-image"
)

// Subnet contains resolved Subnet selector values utilized for node launch
type Subnet struct {
	// ID of the subnet
	// +required
	ID string `json:"id"`
	// The associated availability zone
	// +required
	Zone string `json:"zone"`
	// The associated availability zone ID
	// +optional
	ZoneID string `json:"zoneID,omitempty"`
}

// SecurityGroup contains resolved SecurityGroup selector values utilized for node launch
type SecurityGroup struct {
	// ID of the security group
	// +required
	ID string `json:"id"`
	// Name of the security group
	// +optional
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
type OpenshiftEC2NodeClassSpec struct {
	// SubnetSelectorTerms is a list of or subnet selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="subnetSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id']",rule="self.all(x, has(x.tags) || has(x.id))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in subnetSelectorTerms",rule="!self.all(x, has(x.id) && has(x.tags))"
	// +kubebuilder:validation:MaxItems:=30
	// +optional
	SubnetSelectorTerms []SubnetSelectorTerm `json:"subnetSelectorTerms,omitempty"`

	// SecurityGroupSelectorTerms is a list of or security group selector terms. The terms are ORed.
	// +kubebuilder:validation:XValidation:message="securityGroupSelectorTerms cannot be empty",rule="self.size() != 0"
	// +kubebuilder:validation:XValidation:message="expected at least one, got none, ['tags', 'id', 'name']",rule="self.all(x, has(x.tags) || has(x.id) || has(x.name))"
	// +kubebuilder:validation:XValidation:message="'id' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.id) && (has(x.tags) || has(x.name)))"
	// +kubebuilder:validation:XValidation:message="'name' is mutually exclusive, cannot be set with a combination of other fields in securityGroupSelectorTerms",rule="!self.all(x, has(x.name) && (has(x.tags) || has(x.id)))"
	// +kubebuilder:validation:MaxItems:=30
	// +optional
	SecurityGroupSelectorTerms []SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms,omitempty"`

	// AssociatePublicIPAddress controls if public IP addresses are assigned to instances that are launched with the nodeclass.
	// +optional
	AssociatePublicIPAddress *bool `json:"associatePublicIPAddress,omitempty"`

	// Tags to be applied on ec2 resources like instances and launch templates.
	// +kubebuilder:validation:XValidation:message="empty tag keys aren't supported",rule="self.all(k, k != '')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching eks:eks-cluster-name",rule="self.all(k, k !='eks:eks-cluster-name')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching kubernetes.io/cluster/",rule="self.all(k, !k.startsWith('kubernetes.io/cluster') )"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodepool",rule="self.all(k, k != 'karpenter.sh/nodepool')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.sh/nodeclaim",rule="self.all(k, k !='karpenter.sh/nodeclaim')"
	// +kubebuilder:validation:XValidation:message="tag contains a restricted tag matching karpenter.k8s.aws/ec2nodeclass",rule="self.all(k, k !='karpenter.k8s.aws/ec2nodeclass')"
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// BlockDeviceMappings to be applied to provisioned nodes.
	// +kubebuilder:validation:XValidation:message="must have only one blockDeviceMappings with rootVolume",rule="self.filter(x, has(x.rootVolume)?x.rootVolume==true:false).size() <= 1"
	// +kubebuilder:validation:MaxItems:=50
	// +optional
	BlockDeviceMappings []*BlockDeviceMapping `json:"blockDeviceMappings,omitempty"`

	// InstanceStorePolicy specifies how to handle instance-store disks.
	// +optional
	InstanceStorePolicy *InstanceStorePolicy `json:"instanceStorePolicy,omitempty"`

	// DetailedMonitoring controls if detailed monitoring is enabled for instances that are launched
	// +optional
	DetailedMonitoring *bool `json:"detailedMonitoring,omitempty"`
}

// SubnetSelectorTerm defines selection logic for a subnet used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type SubnetSelectorTerm struct {
	// Tags is a map of key/value tags used to select subnets
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys or values aren't supported",rule="self.all(k, k != '' && self[k] != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the subnet id in EC2
	// +kubebuilder:validation:Pattern="subnet-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`
}

// SecurityGroupSelectorTerm defines selection logic for a security group used by Karpenter to launch nodes.
// If multiple fields are used for selection, the requirements are ANDed.
type SecurityGroupSelectorTerm struct {
	// Tags is a map of key/value tags used to select subnets
	// Specifying '*' for a value selects all values for a given tag key.
	// +kubebuilder:validation:XValidation:message="empty tag keys or values aren't supported",rule="self.all(k, k != '' && self[k] != '')"
	// +kubebuilder:validation:MaxProperties:=20
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the security group id in EC2
	// +kubebuilder:validation:Pattern:="sg-[0-9a-z]+"
	// +optional
	ID string `json:"id,omitempty"`

	// Name is the security group name in EC2.
	// This value is the name field, which is different from the name tag.
	Name string `json:"name,omitempty"`
}

type BlockDeviceMapping struct {
	// The device name (for example, /dev/sdh or xvdh).
	// +optional
	DeviceName *string `json:"deviceName,omitempty"`

	// EBS contains parameters used to automatically set up EBS volumes when an instance is launched.
	// +kubebuilder:validation:XValidation:message="snapshotID or volumeSize must be defined",rule="has(self.snapshotID) || has(self.volumeSize)"
	// +optional
	EBS *BlockDevice `json:"ebs,omitempty"`

	// RootVolume is a flag indicating if this device is mounted as kubelet root dir. You can
	// configure at most one root volume in BlockDeviceMappings.
	RootVolume bool `json:"rootVolume,omitempty"`
}

type BlockDevice struct {
	// DeleteOnTermination indicates whether the EBS volume is deleted on instance termination.
	// +optional
	DeleteOnTermination *bool `json:"deleteOnTermination,omitempty"`

	// Encrypted indicates whether the EBS volume is encrypted. Encrypted volumes can only
	// be attached to instances that support Amazon EBS encryption. If you are creating
	// a volume from a snapshot, you can't specify an encryption value.
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`

	// IOPS is the number of I/O operations per second (IOPS). For gp3, io1, and io2 volumes,
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

	// KMSKeyID (ARN) of the symmetric Key Management Service (KMS) CMK used for encryption.
	// +optional
	KMSKeyID *string `json:"kmsKeyID,omitempty"`

	// SnapshotID is the ID of an EBS snapshot
	// +optional
	SnapshotID *string `json:"snapshotID,omitempty"`

	// Throughput to provision for a gp3 volume, with a maximum of 1,000 MiB/s.
	// Valid Range: Minimum value of 125. Maximum value of 1000.
	// +optional
	Throughput *int64 `json:"throughput,omitempty"`

	// VolumeSize in `Gi`, `G`, `Ti`, or `T`. You must specify either a snapshot ID or
	// a volume size. The following are the supported volumes sizes for each volume
	// type:
	//
	//    * gp2 and gp3: 1-16,384
	//
	//    * io1 and io2: 4-16,384
	//
	//    * st1 and sc1: 125-16,384
	//
	//    * standard: 1-1,024
	// + TODO: Add the CEL resources.quantity type after k8s 1.29
	// + https://github.com/kubernetes/apiserver/commit/b137c256373aec1c5d5810afbabb8932a19ecd2a#diff-838176caa5882465c9d6061febd456397a3e2b40fb423ed36f0cabb1847ecb4dR190
	// +kubebuilder:validation:Pattern:="^((?:[1-9][0-9]{0,3}|[1-4][0-9]{4}|[5][0-8][0-9]{3}|59000)Gi|(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|[6][0-3][0-9]{3}|64000)G|([1-9]||[1-5][0-7]|58)Ti|([1-9]||[1-5][0-9]|6[0-3]|64)T)$"
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type:=string
	// +optional
	VolumeSize *resource.Quantity `json:"volumeSize,omitempty" hash:"string"`

	// VolumeType of the block device.
	// For more information, see Amazon EBS volume types (https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/EBSVolumeTypes.html)
	// in the Amazon Elastic Compute Cloud User Guide.
	// +kubebuilder:validation:Enum:={standard,io1,io2,gp2,sc1,st1,gp3}
	// +optional
	VolumeType *string `json:"volumeType,omitempty"`
}

// OpenshiftEC2NodeClassStatus defines the observed state of OpenshiftEC2NodeClass.
type OpenshiftEC2NodeClassStatus struct {
	// Subnets contains the current Subnet values that are available to the
	// cluster under the subnet selectors.
	// +optional
	Subnets []Subnet `json:"subnets,omitempty"`

	// SecurityGroups contains the current Security Groups values that are available to the
	// cluster under the SecurityGroups selectors.
	// +optional
	SecurityGroups []SecurityGroup `json:"securityGroups,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// Conditions contain signals for health and readiness.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=openshiftec2nodeclasses,shortName=oec2nc;oec2ncs,scope=Cluster
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// OpenshiftEC2NodeClass defines the desired state of OpenshiftEC2NodeClass.
type OpenshiftEC2NodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenshiftEC2NodeClassSpec   `json:"spec,omitempty"`
	Status OpenshiftEC2NodeClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// OpenshiftEC2NodeClassList contains a list of OpenshiftEC2NodeClass.
type OpenshiftEC2NodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenshiftEC2NodeClass `json:"items"`
}
