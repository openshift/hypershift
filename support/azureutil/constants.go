package azureutil

import "time"

const (
	// PollTimeout is the timeout for Azure long-running operations (PollUntilDone)
	// to prevent reconciler goroutines from blocking indefinitely.
	PollTimeout = 5 * time.Minute

	// DriftDetectionRequeueInterval is the interval for periodic reconciliation
	// to detect out-of-band changes to Azure resources.
	DriftDetectionRequeueInterval = 5 * time.Minute

	// PLSRequeueInterval is the interval used when requeuing because a prerequisite
	// (PLS alias, KAS hostname, Private Endpoint IP, or LoadBalancerIP) is not yet available.
	PLSRequeueInterval = 30 * time.Second

	// AzureResourceNameMaxLength is the maximum length for Azure resource names.
	AzureResourceNameMaxLength = 80
)
