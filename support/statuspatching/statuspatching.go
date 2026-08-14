// Package statuspatching provides helpers for safely mutating Kubernetes
// status subresources using optimistic locking.
//
// Multiple HyperShift controllers (CPO, HCCO, HO, karpenter-operator) write
// to shared status objects such as HostedControlPlane. Without optimistic
// locking, concurrent writers silently overwrite each other's changes.
// These helpers enforce the correct pattern: deep-copy before mutation,
// skip no-op updates, and always use MergeFromWithOptimisticLock so that
// stale writes return a conflict error instead of succeeding silently.
package statuspatching

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PatchStatus re-fetches the object, applies mutate, and patches with
// MergeFromWithOptimisticLock. On conflict the whole cycle is retried
// automatically, so callers never need to handle 409s themselves.
// mutate must only modify status fields on obj.
func PatchStatus(ctx context.Context, c client.Client, obj client.Object, mutate func() error) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		original := obj.DeepCopyObject().(client.Object)
		if err := mutate(); err != nil {
			return err
		}
		if equality.Semantic.DeepEqual(original, obj) {
			return nil
		}
		return c.Status().Patch(ctx, obj, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	})
}

// PatchStatusCondition sets a single condition using optimistic-lock patching.
// It uses SetStatusCondition's own change detection to skip no-ops reliably,
// avoiding false positives from LastTransitionTime being stamped with time.Now().
// Pass a pointer to the object's conditions slice (e.g. &hcp.Status.Conditions)
// since HCP types expose conditions as a bare field, not via getter/setter methods.
func PatchStatusCondition(ctx context.Context, c client.Client, obj client.Object, conditions *[]metav1.Condition, condition metav1.Condition) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		original := obj.DeepCopyObject().(client.Object)
		changed := meta.SetStatusCondition(conditions, condition)
		if !changed {
			return nil
		}
		return c.Status().Patch(ctx, obj, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	})
}
