package v1alpha1

// HostedCluster conditions.
const (
	// HostedClusterAvailable indicates whether the HostedCluster has a healthy
	// control plane.
	HostedClusterAvailable ConditionType = "Available"
	// HostedClusterProgressing indicates whether the HostedCluster is attempting
	// an initial deployment or upgrade.
	HostedClusterProgressing ConditionType = "Progressing"
	// HostedClusterDegraded indicates whether the HostedCluster is encountering
	// an error that may require user intervention to resolve.
	HostedClusterDegraded ConditionType = "Degraded"

	// Bubble up from HCP.

	// InfrastructureReady bubbles up the same condition from HCP.
	InfrastructureReady ConditionType = "InfrastructureReady"
	// KubeAPIServerAvailable bubbles up the same condition from HCP.
	KubeAPIServerAvailable ConditionType = "KubeAPIServerAvailable"
	// EtcdAvailable bubbles up the same condition from HCP.
	EtcdAvailable ConditionType = "EtcdAvailable"
	// ValidHostedControlPlaneConfiguration bubbles up the same condition from HCP.
	ValidHostedControlPlaneConfiguration ConditionType = "ValidHostedControlPlaneConfiguration"

	// Bubble up from HCP which bubbles up from CVO.

	// ClusterVersionSucceeding indicates the current status of the desired release
	// version of the HostedCluster as indicated by the Failing condition in the
	// underlying cluster's ClusterVersion.
	ClusterVersionSucceeding ConditionType = "ClusterVersionSucceeding"
	// ClusterVersionUpgradeable indicates the Upgradeable condition in the
	// underlying cluster's ClusterVersion.
	ClusterVersionUpgradeable ConditionType = "ClusterVersionUpgradeable"
	// ClusterVersionFailing bubbles up Failing from the CVO.
	ClusterVersionFailing ConditionType = "ClusterVersionFailing"
	// ClusterVersionProgressing bubbles up configv1.OperatorProgressing from the CVO.
	ClusterVersionProgressing ConditionType = "ClusterVersionProgressing"
	// ClusterVersionAvailable bubbles up Failing configv1.OperatorAvailable from the CVO.
	ClusterVersionAvailable ConditionType = "ClusterVersionAvailable"
	// ClusterVersionReleaseAccepted bubbles up Failing ReleaseAccepted from the CVO.
	ClusterVersionReleaseAccepted ConditionType = "ClusterVersionReleaseAccepted"

	// UnmanagedEtcdAvailable indicates whether a user-managed etcd cluster is
	// healthy.
	UnmanagedEtcdAvailable ConditionType = "UnmanagedEtcdAvailable"

	// IgnitionEndpointAvailable indicates whether the ignition server for the
	// HostedCluster is available to handle ignition requests.
	IgnitionEndpointAvailable ConditionType = "IgnitionEndpointAvailable"

	// ValidHostedClusterConfiguration indicates (if status is true) that the
	// ClusterConfiguration specified for the HostedCluster is valid.
	ValidHostedClusterConfiguration ConditionType = "ValidConfiguration"

	// SupportedHostedCluster indicates whether a HostedCluster is supported by
	// the current configuration of the hypershift-operator.
	// e.g. If HostedCluster requests endpointAcess Private but the hypershift-operator
	// is running on a management cluster outside AWS or is not configured with AWS
	// credentials, the HostedCluster is not supported.
	SupportedHostedCluster ConditionType = "SupportedHostedCluster"

	// ValidOIDCConfiguration indicates if an AWS cluster's OIDC condition is
	// detected as invalid.
	ValidOIDCConfiguration ConditionType = "ValidOIDCConfiguration"

	// ValidReleaseImage indicates if the release image set in the spec is valid
	// for the HostedCluster. For example, this can be set false if the
	// HostedCluster itself attempts an unsupported version before 4.9 or an
	// unsupported upgrade e.g y-stream upgrade before 4.11.
	ValidReleaseImage ConditionType = "ValidReleaseImage"

	// PlatformCredentialsFound indicates that credentials required for the
	// desired platform are valid.
	PlatformCredentialsFound ConditionType = "PlatformCredentialsFound"

	// ReconciliationActive indicates if reconciliation of the HostedCluster is
	// active or paused.
	ReconciliationActive ConditionType = "ReconciliationActive"
	// ReconciliationSucceeded indicates if the HostedCluster reconciliation
	// succeeded.
	ReconciliationSucceeded ConditionType = "ReconciliationSucceeded"
)

// Reasons.
const (
	StatusUnknownReason       = "StatusUnknown"
	AsExpectedReason          = "AsExpected"
	NotFoundReason            = "NotFound"
	WaitingForAvailableReason = "waitingForAvailable"
	SecretNotFoundReason      = "SecretNotFound"

	InfraStatusFailureReason           = "InfraStatusFailure"
	WaitingOnInfrastructureReadyReason = "WaitingOnInfrastructureReady"

	EtcdQuorumAvailableReason     = "QuorumAvailable"
	EtcdWaitingForQuorumReason    = "EtcdWaitingForQuorum"
	EtcdStatefulSetNotFoundReason = "StatefulSetNotFound"

	UnmanagedEtcdMisconfiguredReason = "UnmanagedEtcdMisconfigured"
	UnmanagedEtcdAsExpected          = "UnmanagedEtcdAsExpected"

	FromClusterVersionReason = "FromClusterVersion"

	InvalidConfigurationReason            = "InvalidConfiguration"
	KubeconfigWaitingForCreateReason      = "KubeconfigWaitingForCreate"
	UnsupportedHostedClusterReason        = "UnsupportedHostedCluster"
	InsufficientClusterCapabilitiesReason = "InsufficientClusterCapabilities"
	OIDCConfigurationInvalidReason        = "OIDCConfigurationInvalid"
	PlatformCredentialsNotFoundReason     = "PlatformCredentialsNotFound"
	InvalidImageReason                    = "InvalidImage"
)

// Messages.
const (
	// AllIsWellMessage is standard message.
	AllIsWellMessage = "All is well"
)
