package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformgcp "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/gcp"
	"github.com/openshift/hypershift/support/statuspatching"
)

// reconcileGCPCredentialConditions publishes the credential conditions and the
// control plane version used to compute them in the same HC status update. Health
// checks, metrics, and orphan cleanup therefore observe a consistent version and
// validation result, including during deletion when later propagation is skipped.
func (r *HostedClusterReconciler) reconcileGCPCredentialConditions(ctx context.Context, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	if err := statuspatching.PatchStatus(ctx, r.Client, hc, func() error {
		propagateControlPlaneVersion(hc, hcp)
		platformgcp.ComputeGCPCredentialConditions(hc, hcp)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update GCP credential status: %w", err)
	}
	return nil
}
