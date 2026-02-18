//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DeletionTimeout is the default timeout for waiting for deletion
	DeletionTimeout      = 10 * time.Minute
	DeletionRetryTimeout = 5 * time.Minute
	// PollInterval is the interval for polling deletion status
	PollInterval = 5 * time.Second
)

// BreakHostedCluster simulates a catastrophic failure by deleting HC, HCP, and related resources.
// 1. Delete HostedCluster (initiate deletion, don't wait)
// 2. Delete HCP namespace (without waiting)
// 3. Delete HostedControlPlane (with waiting for deletion)
// 4. Wait for HostedCluster deletion (with finalizer removal retry if needed)
func BreakHostedCluster(testCtx *internal.TestContext) error {
	hcName := testCtx.ClusterName
	hcNamespace := testCtx.ClusterNamespace

	// Step 1: Delete the HostedCluster (initiate deletion only)
	if err := DeleteHostedCluster(testCtx); err != nil {
		return fmt.Errorf("failed to delete hosted cluster: %w", err)
	}

	// Step 2: Delete HCP namespace (without waiting for completion)
	if err := DeleteControlPlaneNamespace(testCtx, false); err != nil {
		return fmt.Errorf("failed to delete control plane namespace: %w", err)
	}

	// Step 3: Delete HostedControlPlane
	if err := DeleteHostedControlPlane(testCtx); err != nil {
		return fmt.Errorf("failed to delete hosted control plane: %w", err)
	}

	// Step 4: Wait for HostedCluster deletion
	err := waitForHCDeletion(testCtx, hcName, hcNamespace, DeletionTimeout)
	if err != nil {
		// If waiting timed out, attempt to remove HC finalizers and retry
		if nukeErr := removeHCFinalizers(testCtx, hcName, hcNamespace); nukeErr != nil {
			return fmt.Errorf("failed to wait for HC deletion (timeout: %v) and failed to remove finalizers: %w", err, nukeErr)
		}

		// Retry waiting for deletion after removing finalizers
		retryErr := waitForHCDeletion(testCtx, hcName, hcNamespace, DeletionRetryTimeout)
		if retryErr != nil {
			return fmt.Errorf("failed to wait for HC deletion even after removing finalizers (original timeout: %v, retry error: %w)", err, retryErr)
		}
	}

	return nil
}

// DeleteHostedCluster deletes the HostedCluster without waiting for deletion to complete
func DeleteHostedCluster(testCtx *internal.TestContext) error {
	hcName := testCtx.ClusterName
	hcNamespace := testCtx.ClusterNamespace

	if hcName == "" || hcNamespace == "" {
		return fmt.Errorf("cluster name or namespace is not set in test context")
	}

	hc := &hyperv1.HostedCluster{}
	err := testCtx.MgmtClient.Get(testCtx.Context, types.NamespacedName{
		Namespace: hcNamespace,
		Name:      hcName,
	}, hc)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already deleted
			return nil
		}
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", hcNamespace, hcName, err)
	}

	// Delete the HostedCluster
	if err := testCtx.MgmtClient.Delete(testCtx.Context, hc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete HostedCluster %s/%s: %w", hcNamespace, hcName, err)
		}
	}

	return nil
}

// removeHCFinalizers removes all finalizers from the HostedCluster to force deletion.
// Uses retry logic to handle resource version conflicts.
func removeHCFinalizers(testCtx *internal.TestContext, hcName, hcNamespace string) error {
	return wait.PollUntilContextTimeout(testCtx.Context, 1*time.Second, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			hc := &hyperv1.HostedCluster{}
			err := testCtx.MgmtClient.Get(ctx, types.NamespacedName{
				Namespace: hcNamespace,
				Name:      hcName,
			}, hc)

			if err != nil {
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}

			if len(hc.Finalizers) == 0 {
				return true, nil
			}

			// Remove all finalizers
			hc.Finalizers = []string{}
			if err := testCtx.MgmtClient.Update(ctx, hc); err != nil {
				if apierrors.IsConflict(err) {
					return false, nil // Retry on conflict
				}
				return false, err
			}

			return true, nil
		})
}

// DeleteHostedControlPlane deletes the HostedControlPlane and waits for its deletion.
// It attempts to gracefully delete the HCP, and if it gets stuck, it can optionally
// remove finalizers to force deletion.
func DeleteHostedControlPlane(testCtx *internal.TestContext) error {
	hcpName := testCtx.ClusterName
	hcpNamespace := testCtx.ControlPlaneNamespace

	if hcpNamespace == "" {
		return fmt.Errorf("control plane namespace is not set in test context")
	}

	// Get the HostedControlPlane
	hcp := &hyperv1.HostedControlPlane{}
	err := testCtx.MgmtClient.Get(testCtx.Context, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      hcpName,
	}, hcp)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already deleted
			return nil
		}
		return fmt.Errorf("failed to get HostedControlPlane %s/%s: %w", hcpNamespace, hcpName, err)
	}

	// Delete the HostedControlPlane
	if err := testCtx.MgmtClient.Delete(testCtx.Context, hcp); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete HostedControlPlane %s/%s: %w", hcpNamespace, hcpName, err)
		}
	}

	// Wait for HCP deletion
	return waitForHCPDeletion(testCtx, hcpName, hcpNamespace, DeletionTimeout)
}

// waitForHCPDeletion polls until the HostedControlPlane is deleted or timeout occurs
func waitForHCPDeletion(testCtx *internal.TestContext, hcpName, hcpNamespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		hcp := &hyperv1.HostedControlPlane{}
		err := testCtx.MgmtClient.Get(ctx, types.NamespacedName{
			Namespace: hcpNamespace,
			Name:      hcpName,
		}, hcp)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Resource successfully deleted
				return true, nil
			}
			// Handle retryable errors
			if apierrors.IsTooManyRequests(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
				return false, nil
			}
			return false, fmt.Errorf("unexpected error checking HostedControlPlane deletion: %w", err)
		}

		// Resource still exists
		return false, nil
	})
}

// waitForHCDeletion polls until the HostedCluster is deleted or timeout occurs
func waitForHCDeletion(testCtx *internal.TestContext, hcName, hcNamespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		hc := &hyperv1.HostedCluster{}
		err := testCtx.MgmtClient.Get(ctx, types.NamespacedName{
			Namespace: hcNamespace,
			Name:      hcName,
		}, hc)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Resource successfully deleted
				return true, nil
			}
			// Handle retryable errors
			if apierrors.IsTooManyRequests(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
				return false, nil
			}
			return false, fmt.Errorf("unexpected error checking HostedCluster deletion: %w", err)
		}

		// Resource still exists
		return false, nil
	})
}

// DeleteControlPlaneNamespace deletes the HCP namespace.
// Set waitForDeletion to true if you want to wait for the namespace to be fully removed.
func DeleteControlPlaneNamespace(testCtx *internal.TestContext, waitForDeletion bool) error {
	namespace := testCtx.ControlPlaneNamespace

	if namespace == "" {
		return fmt.Errorf("control plane namespace is not set in test context")
	}

	// Get the namespace first
	nsObj := &corev1.Namespace{}
	err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Name: namespace}, nsObj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to get control plane namespace %s: %w", namespace, err)
	}

	// Delete the namespace
	err = testCtx.MgmtClient.Delete(testCtx.Context, nsObj)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete control plane namespace %s: %w", namespace, err)
	}

	if !waitForDeletion {
		return nil
	}

	// Wait for namespace deletion
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, DeletionTimeout, true, func(ctx context.Context) (bool, error) {
		err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Name: namespace}, nsObj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			if apierrors.IsTooManyRequests(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
				return false, nil
			}
			return false, fmt.Errorf("unexpected error checking namespace deletion: %w", err)
		}
		return false, nil
	})
}
