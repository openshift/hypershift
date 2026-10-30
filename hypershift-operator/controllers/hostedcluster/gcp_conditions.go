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

// validateGCPResourceTagsSupport checks whether requested tags can be propagated
// by the selected CPO image, without changing the HostedCluster. Image-resolution
// failures are reported separately, so unresolved labels are not treated as a
// missing capability.
func validateGCPResourceTagsSupport(hc *hyperv1.HostedCluster, labelsResolved bool, imageLabels map[string]string) error {
	if hc.Spec.Platform.Type != hyperv1.GCPPlatform || hc.Spec.Platform.GCP == nil || len(hc.Spec.Platform.GCP.ResourceTags) == 0 || !labelsResolved {
		return nil
	}
	if _, supported := imageLabels[controlPlaneOperatorSupportsGCPResourceTagsLabel]; !supported {
		return fmt.Errorf("spec.platform.gcp.resourceTags requires a control plane operator image that supports GCP resource tag propagation")
	}
	return nil
}
