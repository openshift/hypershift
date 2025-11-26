package gcp

import "time"

const (
	// defaultOperationTimeout is the timeout for waiting on GCP long-running operations.
	defaultOperationTimeout = 5 * time.Minute

	// defaultPollingInterval is the interval between polling attempts for operation status.
	defaultPollingInterval = 2 * time.Second
)
