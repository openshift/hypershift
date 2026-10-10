package hostedcluster

import (
	"context"
	"errors"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/statuspatching"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *HostedClusterReconciler) patchReconcileStatus(ctx context.Context, hc *hyperv1.HostedCluster, generation int64, reconcileErr error) error {
	return r.patchHostedClusterCondition(ctx, hc, generation, func(target *hyperv1.HostedCluster) metav1.Condition {
		condition := metav1.Condition{
			Type:               string(hyperv1.ReconciliationSucceeded),
			ObservedGeneration: target.Generation,
			Status:             metav1.ConditionTrue,
			Reason:             "ReconciliatonSucceeded",
			Message:            "Reconciliation completed successfully",
			LastTransitionTime: r.now(),
		}
		if reconcileErr != nil {
			condition.Status = metav1.ConditionFalse
			condition.Reason = "ReconciliationError"
			condition.Message = reconcileErr.Error()
		}
		return condition
	})
}

// patchHostedClusterCondition changes only the condition computed from the fresh
// target. Never replay pending status or import unrelated server status into hc.
func (r *HostedClusterReconciler) patchHostedClusterCondition(ctx context.Context, hc *hyperv1.HostedCluster, generation int64, compute func(*hyperv1.HostedCluster) metav1.Condition) error {
	target := hc.DeepCopy()
	var conditionType string
	err := statuspatching.PatchStatus(ctx, r.Client, target, func() error {
		if target.Generation != generation {
			return errors.New("HostedCluster changed during reconciliation")
		}
		if !sameReconcileMetadata(hc.ObjectMeta, target.ObjectMeta) {
			return errors.New("HostedCluster metadata changed during reconciliation")
		}
		condition := compute(target)
		conditionType = condition.Type
		meta.SetStatusCondition(&target.Status.Conditions, condition)
		return nil
	})
	if err == nil {
		statuspatching.SyncCondition(target.Status.Conditions, &hc.Status.Conditions, conditionType)
		// Do not promote the working object's RV: its other status fields may be
		// stale. Any later whole-status write must conflict rather than overwrite
		// values observed only by the detached patch target.
	}
	return err
}

func sameReconcileMetadata(before, after metav1.ObjectMeta) bool {
	before.ResourceVersion = after.ResourceVersion
	before.ManagedFields = after.ManagedFields
	return equality.Semantic.DeepEqual(before, after)
}
