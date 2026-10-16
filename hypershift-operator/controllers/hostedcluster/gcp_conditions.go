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

func (r *HostedClusterReconciler) reconcileGCPResourceTagsSupportCondition(ctx context.Context, hc *hyperv1.HostedCluster, supported bool) error {
	generation := hc.Generation
	if err := statuspatching.PatchStatus(ctx, r.Client, hc, func() error {
		if hc.Generation != generation {
			return nil
		}
		platformgcp.ComputeGCPResourceTagsSupportCondition(hc, supported)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update GCP resource tag support status: %w", err)
	}
	return nil
}

// validateGCPResourceTagsSupport verifies that a requested resourceTags list can
// be propagated by the selected control plane operator image. It intentionally
// does nothing when tags are omitted: resource tags are optional.
func (r *HostedClusterReconciler) validateGCPResourceTagsSupport(ctx context.Context, hc *hyperv1.HostedCluster, labelsResolved bool, imageLabels map[string]string) error {
	if hc.Spec.Platform.GCP == nil || len(hc.Spec.Platform.GCP.ResourceTags) == 0 || !labelsResolved {
		return nil
	}

	_, supported := imageLabels[controlPlaneOperatorSupportsGCPResourceTagsLabel]
	if err := r.reconcileGCPResourceTagsSupportCondition(ctx, hc, supported); err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("spec.platform.gcp.resourceTags requires a control plane operator image that supports GCP resource tag propagation")
	}
	return nil
}
