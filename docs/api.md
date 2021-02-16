# HyperShift API Reference

A HyperShift cluster is represented by a `HostedCluster` and zero to many related `NodePool` resources.

## HostedCluster

```go
// HostedCluster is a managed OpenShift installation.
type HostedCluster struct {
    Spec HostedClusterSpec
    Status HostedClusterStatus
}

// HostedClusterSpec
type HostedClusterSpec struct {
    // clusterID uniquely identifies this cluster. This is expected to be
    // an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in
    // hexadecimal values). This is a required field.
    // +kubebuilder:validation:Required
    // +required
    ClusterID ClusterID `json:"clusterID"`

    Release Release

    Networking ClusterNetworking

    // NOTE: This might not make sense as control plane
    // inputs can be specific to versions
    ControlPlane ControlPlaneSpec

    // PullSecret is propagated to the container runtime
    // of any nodes associated with this cluster.
    PullSecret LocalObjectReference

    // ProviderCreds is a reference to a secret containing cloud account info
    ProviderCreds LocalObjectReference

    // Platform contains platform-specific details for
    // the cluster
    Platform PlatformSpec `json:"platform"`

    // InfraID is used to identify the cluster in cloud platforms
    InfraID string `json:"infraID,omitempty"`
}

type ClusterNetworking struct {
    PodCIDR string
    ServiceCIDR string
    MachineCIDR string
}

type PlatformSpec struct {
    // AWS contains AWS-specific settings for the HostedCluster
    AWS *AWSPlatformSpec `json:"aws"`
}

type AWSPlatformSpec struct {

    // Region is the AWS region for the cluster
    Region string

    // VPC specifies the VPC used for the cluster
    VPC string `json:"vpc"`

    // NodePoolDefaults specifies the default platform
    NodePoolDefaults *AWSNodePoolPlatform `json:"nodePoolDefaults"`

    // ServiceEndpoints list contains custom endpoints which will override default
    // service endpoint of AWS Services.
    // There must be only one ServiceEndpoint for a service.
    // +optional
    ServiceEndpoints []AWSServiceEndpoint `json:"serviceEndpoints,omitempty"`
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

// TODO - block for auth, cidrBlocks
type ControlPlaneSpec struct {
    // Config references a configmap that contains
    // parameters for the control plane
    Config LocalObjectReference
}

type ControlPlaneAuthSpec struct {
    ClientCert LocalObjectReference
    ClientKey LocalObjectReference
    ClusterCACert LocalObjectReference
}

// TODO: Multiple ways to consider handling optionality,
// including, need to choose. Here are some:
// - Pointers: non-nil value means enabled
// - Values: nested 'Enabled' field
type AddonsSpec struct {
    Console *ConsoleAddonSpec
    AutoScaler *AutoScalerAddonSpec
    IdentityProviders *IdentityProviderAddonSpec
    Telemetry *TelemetryAddonSpec
    Monitoring *MonitoringAddonSpec
    Insights *InsightsAddonSpec
    OLM *OLMAddonSpec
}

// TODO maybe we have profiles for scaling behaviors
type AutoScalerAddonSpec struct {
    ResourceLimits LimitRange
}

type HostedClusterStatus struct {
    Version ClusterVersionStatus
    Endpoint string
    Conditions []HostedClusterCondition

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
    History []UpdateHistory `json:"history,omitempty"`

    // observedGeneration reports which version of the spec is being synced.
    // If this value is not equal to metadata.generation, then the desired
    // and conditions fields may represent a previous version.
    // +kubebuilder:validation:Required
    // +required
    ObservedGeneration int64 `json:"observedGeneration"`
}

// Release represents an OpenShift release image and associated metadata.
// +k8s:deepcopy-gen=true
type Release struct {
    // image is a container image location that contains the update. When this
    // field is part of spec, image is optional if version is specified and the
    // availableUpdates field contains a matching version.
    // +required
    Image string `json:"image"`
}

// UpdateHistory is a single attempted update to the cluster.
type UpdateHistory struct {
    // state reflects whether the update was fully applied. The Partial state
    // indicates the update is not fully applied, while the Completed state
    // indicates the update was successfully rolled out at least once (all
    // parts of the update successfully applied).
    // +kubebuilder:validation:Required
    // +required
    State UpdateState `json:"state"`

    // startedTime is the time at which the update was started.
    // +kubebuilder:validation:Required
    // +required
    StartedTime metav1.Time `json:"startedTime"`

    // completionTime, if set, is when the update was fully applied. The update
    // that is currently being applied will have a null completion time.
    // Completion time will always be set for entries that are not the current
    // update (usually to the started time of the next update).
    // +kubebuilder:validation:Required
    // +required
    // +nullable
    CompletionTime *metav1.Time `json:"completionTime"`

    // version is a semantic versioning identifying the update version. If the
    // requested image does not define a version, or if a failure occurs
    // retrieving the image, this value may be empty.
    //
    // +optional
    Version string `json:"version"`

    // image is a container image location that contains the update. This value
    // is always populated.
    // +kubebuilder:validation:Required
    // +required
    Image string `json:"image"`
}

```

## NodePool

TODO:
- How is this associated with a HostedCluster?

```go
// NodePool is a set of nodes owned by a HostedCluster.
type NodePool struct {
    Spec NodePoolSpec
    Status NodePoolStatus
}

type NodePoolSpec struct {
    // TODO: do we really want this for now? It would
    // contain kernel arguments, initial ignition
    // config, etc. which could perhaps be instead
    // configured day 2 through machine config daemon.
    MachineConfig MachineConfigSpec

    Template MachineConfigTemplate

    Platform NodePoolPlatform
}

// NodePoolPlatform is the platform-specific configuration for a node
// pool. Only one of the platforms should be set.
type NodePoolPlatform struct {
	// AWS is the configuration used when installing on AWS.
	AWS *AWSNodePoolPlatform `json:"aws,omitempty"`
}

// AWSNodePoolPlatform stores the configuration for a node pool
// installed on AWS.
type AWSNodePoolPlatform struct {
	// InstanceType defines the ec2 instance type.
	// eg. m4-large
	Zone            string                 `json:"zone"`
	InstanceType    string                 `json:"instanceType"`
	InstanceProfile string                 `json:"instanceProfile,omitempty"`
	Subnet          *AWSResourceReference  `json:"subnet,omitempty"`
	SecurityGroups  []AWSResourceReference `json:"securityGroups,omitempty"`
	AMI             string                 `json:"ami"`
}

// AWSResourceReference is a reference to a specific AWS resource by ID, ARN, or filters.
// Only one of ID, ARN or Filters may be specified. Specifying more than one will result in
// a validation error.
type AWSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// ARN of resource
	// +optional
	ARN *string `json:"arn,omitempty"`

	// Filters is a set of key/value pairs used to identify a resource
	// They are applied according to the rules defined by the AWS API:
	// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Filtering.html
	// +optional
	Filters []Filter `json:"filters,omitempty"`
}

type MachineConfigTemplate struct {
    Provider ProviderMachineConfigSpec

    // ? <xyz> | used in boot-your-self flow
    MachineClass string

    InitialNodeCount int

    AutoScaling *MachineAutoScalingSpec

    Management MachineManagementSpec
}

// A union type
type ProviderMachineConfigSpec struct {
    AWS *AWSProviderMachineConfigSpec
}

type AWSProviderMachineConfigSpec struct {
    InstanceType string
    IAMInstanceProfile string
    SecurityGroups []string
    // Subnet (id, az), etc.
    Network AWSMachineNetwork
}

type MachineAutoScalingSpec struct {
    Min int
    Max int
}

type MachineManagementSpec struct {
    Upgrades MachineUpgradePolicySpec

    // drives use of machine health check
    Repair MachineRepairPolicySpec
}

// A union type
type MachineUpgradePolicySpec struct {
    InPlace *InPlaceMachineUpgradePolicySpec
    Rolling *RollingMachineUpgradePolicySpec
}

// InPlaceMachineUpgradePolicySpec uses the machine
// config daemon and requires no surge capacity
type InPlaceMachineUpgradePolicySpec struct {}

// Rolling updates use surge capacity to mint new
// parallel nodes and obviates the need for machine
// config daemon
type RollingMachineUpgradePolicySpec struct {
    MaxSurge int
}

// A union type
type MachineRepairPolicyType struct {
    Automatic *AutomaticMachineRepairPolicySpec
}

type AutomaticMachineRepairPolicySpec struct {}

type NodePoolStatus {
    Conditions []NodePoolCondition
}
```
