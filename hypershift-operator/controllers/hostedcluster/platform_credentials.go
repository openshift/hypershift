package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformkubevirt "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/pkg/manifests"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// reconcileKubevirtCredentialSafety rejects unsafe external KubeVirt credentials
// and removes an unsafe target copied by an older operator. It runs before
// infrastructure discovery; publication happens later in
// reconcilePlatformCredentialsWithStatus once the control plane namespace exists.
// The PlatformCredentialsFound condition set here is persisted by the status
// update at the end of Reconcile.
func (r *HostedClusterReconciler) reconcileKubevirtCredentialSafety(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return nil
	}
	p := platformkubevirt.New(nil)
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	if !hc.DeletionTimestamp.IsZero() {
		// Teardown must not depend on the tenant source still being present or valid.
		return p.ReconcileDeletionCredentials(ctx, r.Client, hc, controlPlaneNamespace)
	}
	if _, err := p.ValidateCredentials(ctx, r.Client, hc, controlPlaneNamespace); err != nil {
		setPlatformCredentialsNotFound(hc, err)
		return fmt.Errorf("failed to reconcile platform credentials: %w", err)
	}
	return nil
}

func setPlatformCredentialsNotFound(hc *hyperv1.HostedCluster, err error) {
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.PlatformCredentialsFound),
		Status:             metav1.ConditionFalse,
		Reason:             hyperv1.PlatformCredentialsNotFoundReason,
		ObservedGeneration: hc.Generation,
		Message:            err.Error(),
	})
}
