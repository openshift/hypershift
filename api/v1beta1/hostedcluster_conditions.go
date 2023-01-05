package v1beta1

// "Condition values may change back and forth, but some condition transitions may be monotonic, depending on the resource and condition type.
// However, conditions are observations and not, themselves, state machines."
// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

// Conditions.
const (
	// HostedClusterAvailable indicates whether the HostedCluster has a healthy
	// control plane.
	// When this is false for too long and there's no clear indication in the "Reason", please check the remaining more granular conditions.
	HostedClusterAvailable ConditionType = "Available"
	// HostedClusterProgressing indicates whether the HostedCluster is attempting
	// an initial deployment or upgrade.
	// When this is false for too long and there's no clear indication in the "Reason", please check the remaining more granular conditions.
	HostedClusterProgressing ConditionType = "Progressing"
	// HostedClusterDegraded indicates whether the HostedCluster is encountering
	// an error that may require user intervention to resolve.
	HostedClusterDegraded ConditionType = "Degraded"

	// Bubble up from HCP.

	// InfrastructureReady bubbles up the same condition from HCP. It signals if the infrastructure for a control plane to be operational,
	// e.g. load balancers were created successfully.
	// A failure here may require external user intervention to resolve. E.g. hitting quotas on the cloud provider.
	InfrastructureReady ConditionType = "InfrastructureReady"
	// KubeAPIServerAvailable bubbles up the same condition from HCP. It signals if the kube API server is available.
	// A failure here often means a software bug or a non-stable cluster.
	KubeAPIServerAvailable ConditionType = "KubeAPIServerAvailable"
	// EtcdAvailable bubbles up the same condition from HCP. It signals if etcd is available.
	// A failure here often means a software bug or a non-stable cluster.
	EtcdAvailable ConditionType = "EtcdAvailable"
	// ValidHostedControlPlaneConfiguration bubbles up the same condition from HCP. It signals if the hostedControlPlane input is valid and
	// supported by the underlying management cluster.
	// A failure here is unlikely to resolve without the changing user input.
	ValidHostedControlPlaneConfiguration ConditionType = "ValidHostedControlPlaneConfiguration"
	// CloudResourcesDestroyed bubbles up the same condition from HCP. It signals if the cloud provider infrastructure created by Kubernetes
	// in the consumer cloud provider account was destroyed.
	// A failure here may require external user intervention to resolve. E.g. cloud provider perms were corrupted. E.g. the guest cluster was broken
	// and kube resource deletion that affects cloud infra like service type load balancer can't succeed.
	CloudResourcesDestroyed ConditionType = "CloudResourcesDestroyed"

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
	// A failure here often means a software bug or a non-stable cluster.
	IgnitionEndpointAvailable ConditionType = "IgnitionEndpointAvailable"

	// ValidHostedClusterConfiguration signals if the hostedCluster input is valid and
	// supported by the underlying management cluster.
	// A failure here is unlikely to resolve without the changing user input.
	ValidHostedClusterConfiguration ConditionType = "ValidConfiguration"

	// SupportedHostedCluster indicates whether a HostedCluster is supported by
	// the current configuration of the hypershift-operator.
	// e.g. If HostedCluster requests endpointAcess Private but the hypershift-operator
	// is running on a management cluster outside AWS or is not configured with AWS
	// credentials, the HostedCluster is not supported.
	// A failure here is unlikely to resolve without the changing user input.
	SupportedHostedCluster ConditionType = "SupportedHostedCluster"

	// ValidOIDCConfiguration indicates if an AWS cluster's OIDC condition is
	// detected as invalid.
	// A failure here may require external user intervention to resolve. E.g. oidc was deleted out of band.
	ValidOIDCConfiguration ConditionType = "ValidOIDCConfiguration"

	// ValidReleaseImage indicates if the release image set in the spec is valid
	// for the HostedCluster. For example, this can be set false if the
	// HostedCluster itself attempts an unsupported version before 4.9 or an
	// unsupported upgrade e.g y-stream upgrade before 4.11.
	// A failure here is unlikely to resolve without the changing user input.
	ValidReleaseImage ConditionType = "ValidReleaseImage"

	// ValidAWSIdentityProvider indicates if the Identity Provider referenced
	// in the cloud credentials is healthy. E.g. for AWS the idp ARN is referenced in the iam roles.
	// 		"Version": "2012-10-17",
	//		"Statement": [
	//			{
	//				"Effect": "Allow",
	//				"Principal": {
	//					"Federated": "{{ .ProviderARN }}"
	//				},
	//					"Action": "sts:AssumeRoleWithWebIdentity",
	//				"Condition": {
	//					"StringEquals": {
	//						"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
	//					}
	//				}
	//			}
	//		]
	//
	// A failure here may require external user intervention to resolve.
	ValidAWSIdentityProvider ConditionType = "ValidAWSIdentityProvider"

	// PlatformCredentialsFound indicates that credentials required for the
	// desired platform are valid.
	// A failure here is unlikely to resolve without the changing user input.
	PlatformCredentialsFound ConditionType = "PlatformCredentialsFound"

	// ReconciliationActive indicates if reconciliation of the HostedCluster is
	// active or paused hostedCluster.spec.pausedUntil.
	ReconciliationActive ConditionType = "ReconciliationActive"
	// ReconciliationSucceeded indicates if the HostedCluster reconciliation
	// succeeded.
	// A failure here often means a software bug or a non-stable cluster.
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
	InvalidIdentityProvider               = "InvalidIdentityProvider"
)

// Messages.
const (
	// AllIsWellMessage is standard message.
	AllIsWellMessage = "All is well"
)
