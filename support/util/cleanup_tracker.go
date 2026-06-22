package util

import (
	"context"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// MaxCleanupFailures is the maximum number of consecutive connection failures before skipping cleanup
	MaxCleanupFailures = 5
	// MaxCleanupFailureDuration is the maximum duration of connection failures before skipping cleanup
	MaxCleanupFailureDuration = 5 * time.Minute
)

// cleanupFailureTracker tracks connection failures during cloud resource cleanup for a specific HCP
type cleanupFailureTracker struct {
	count            int
	firstFailureTime time.Time
}

// CleanupTracker manages cleanup failure tracking for multiple HCPs
type CleanupTracker struct {
	failures map[string]*cleanupFailureTracker
	mutex    sync.RWMutex
}

// NewCleanupTracker creates a new CleanupTracker
func NewCleanupTracker() *CleanupTracker {
	return &CleanupTracker{
		failures: make(map[string]*cleanupFailureTracker),
	}
}

// RecordFailure increments the failure count for a given HCP
func (t *CleanupTracker) RecordFailure(hcpKey string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	tracker, exists := t.failures[hcpKey]
	if !exists {
		tracker = &cleanupFailureTracker{}
		t.failures[hcpKey] = tracker
	}
	if tracker.count == 0 {
		tracker.firstFailureTime = time.Now()
	}
	tracker.count++
}

// ResetFailures resets the failure tracking for a given HCP
func (t *CleanupTracker) ResetFailures(hcpKey string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	delete(t.failures, hcpKey)
}

// ShouldSkipCleanup checks if cleanup should be skipped based on failure count, duration, or KAS availability
func (t *CleanupTracker) ShouldSkipCleanup(ctx context.Context, hcp *hyperv1.HostedControlPlane, cpClient client.Client) (bool, string, error) {
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// First check if KubeAPIServer deployment exists
	kasAvailable, err := isKubeAPIServerAvailable(ctx, hcp, cpClient)
	if err != nil {
		return false, "", err
	}
	if !kasAvailable {
		return true, "KubeAPIServerUnavailable", nil
	}

	// Check failure tracking
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	tracker, exists := t.failures[hcpKey]
	if !exists || tracker.count == 0 {
		return false, "", nil
	}

	// Skip if max failures exceeded
	if tracker.count >= MaxCleanupFailures {
		return true, "MaxConnectionFailuresExceeded", nil
	}

	// Skip if max duration exceeded
	if time.Since(tracker.firstFailureTime) >= MaxCleanupFailureDuration {
		return true, "ConnectionFailureTimeout", nil
	}

	return false, "", nil
}

// GetFailureCount returns the current failure count for a given HCP
func (t *CleanupTracker) GetFailureCount(hcpKey string) int {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	tracker, exists := t.failures[hcpKey]
	if !exists {
		return 0
	}
	return tracker.count
}

// GetFirstFailureTime returns the first failure time for a given HCP
func (t *CleanupTracker) GetFirstFailureTime(hcpKey string) time.Time {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	tracker, exists := t.failures[hcpKey]
	if !exists {
		return time.Time{}
	}
	return tracker.firstFailureTime
}

// isKubeAPIServerAvailable checks if the kube-apiserver deployment exists in the control plane namespace.
// Returns false if the deployment is not found, true if it exists.
func isKubeAPIServerAvailable(ctx context.Context, hcp *hyperv1.HostedControlPlane, cpClient client.Client) (bool, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: hcp.Namespace,
		},
	}
	if err := cpClient.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
