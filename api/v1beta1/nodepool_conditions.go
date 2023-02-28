package v1beta1

// Conditions
const (
	// NodePoolValidGeneratedPayloadConditionType signals if the ignition sever generated an ignition payload successfully for Nodes in that pool.
	// A failure here often means a software bug or a non-stable cluster.
	NodePoolValidGeneratedPayloadConditionType = "ValidGeneratedPayload"
	// NodePoolValidPlatformImageType signals if an OS image e.g. an AMI was found successfully based on the consumer input e.g. releaseImage.
	// If the image is direct user input then this condition is meaningless.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolValidPlatformImageType = "ValidPlatformImage"
	// NodePoolValidReleaseImageConditionType signals if the input in nodePool.spec.release.image is valid.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolValidReleaseImageConditionType = "ValidReleaseImage"
	// NodePoolValidMachineConfigConditionType signals if the content within nodePool.spec.config is valid.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolValidMachineConfigConditionType = "ValidMachineConfig"
	// NodePoolValidTuningConfigConditionType signals if the content within nodePool.spec.tuningConfig is valid.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolValidTuningConfigConditionType = "ValidTuningConfig"

	// NodePoolUpdateManagementEnabledConditionType signals if the nodePool.spec.management input is valid.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolUpdateManagementEnabledConditionType = "UpdateManagementEnabled"
	// NodePoolAutoscalingEnabledConditionType signals if nodePool.spec.replicas and nodePool.spec.AutoScaling input is valid.
	// A failure here is unlikely to resolve without the changing user input.
	NodePoolAutoscalingEnabledConditionType = "AutoscalingEnabled"
	// NodePoolAutorepairEnabledConditionType signals if MachineHealthChecks resources were created successfully.
	// A failure here often means a software bug or a non-stable cluster.
	NodePoolAutorepairEnabledConditionType = "AutorepairEnabled"

	// NodePoolUpdatingVersionConditionType signals if a version update is currently happening in NodePool.
	NodePoolUpdatingVersionConditionType = "UpdatingVersion"
	// NodePoolUpdatingConfigConditionType signals if a config update is currently happening in NodePool.
	NodePoolUpdatingConfigConditionType = "UpdatingConfig"

	// NodePoolReadyConditionType bubbles up CAPI MachineDeployment/MachineSet Ready condition.
	// This is true when all replicas are ready Nodes.
	// When this is false for too long, NodePoolAllMachinesReadyConditionType and NodePoolAllNodesHealthyConditionType might provide more context.
	NodePoolReadyConditionType = "Ready"
	// NodePoolAllMachinesReadyConditionType bubbles up and aggregates CAPI Machine Ready condition.
	// It signals when the infrastructure for a Machine resource was created successfully.
	// https://github.com/kubernetes-sigs/cluster-api/blob/main/api/v1beta1/condition_consts.go
	// A failure here may require external user intervention to resolve. E.g. hitting quotas on the cloud provider.
	NodePoolAllMachinesReadyConditionType = "AllMachinesReady"
	// NodePoolAllNodesHealthyConditionType bubbles up and aggregates CAPI NodeHealthy condition.
	// It signals when the Node for a Machine resource is healthy.
	// https://github.com/kubernetes-sigs/cluster-api/blob/main/api/v1beta1/condition_consts.go
	// A failure here often means a software bug or a non-stable cluster.
	NodePoolAllNodesHealthyConditionType = "AllNodesHealthy"

	// NodePoolReconciliationActiveConditionType signals the state of nodePool.spec.pausedUntil.
	NodePoolReconciliationActiveConditionType = "ReconciliationActive"

	// NodePoolReachedIgnitionEndpoint signals if at least an instance was able to reach the ignition endpoint to get the payload.
	// When this is false for too long it may require external user intervention to resolve. E.g. Enable AWS security groups to enable networking access.
	NodePoolReachedIgnitionEndpoint = "ReachedIgnitionEndpoint"

	// NodePoolAWSSecurityGroupAvailableConditionType signals whether the NodePool has an available security group to use.
	// If the security group is specified for the NodePool, this condition is always true. If no security group is specified
	// for the NodePool, the status of this condition depends on the availability of the default security group in the HostedCluster.
	NodePoolAWSSecurityGroupAvailableConditionType = "AWSSecurityGroupAvailable"
)

// Reasons
const (
	NodePoolValidationFailedReason        = "ValidationFailed"
	NodePoolInplaceUpgradeFailedReason    = "InplaceUpgradeFailed"
	NodePoolNotFoundReason                = "NotFound"
	NodePoolFailedToGetReason             = "FailedToGet"
	IgnitionEndpointMissingReason         = "IgnitionEndpointMissing"
	IgnitionCACertMissingReason           = "IgnitionCACertMissing"
	IgnitionNotReached                    = "ignitionNotReached"
	DefaultAWSSecurityGroupNotReadyReason = "DefaultSGNotReady"
)
