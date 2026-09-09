// Package statuspatching provides helpers for safely mutating Kubernetes
// status subresources using optimistic locking.
//
// Multiple HyperShift controllers (CPO, HCCO, HO, karpenter-operator) write
// to shared status objects such as HostedControlPlane. Without optimistic
// locking, concurrent writers silently overwrite each other's changes.
// These helpers enforce the correct pattern: deep-copy before mutation,
// skip no-op updates, and use optimistic locking so that stale writes return
// a conflict error instead of succeeding silently.
package statuspatching

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

// PatchStatusWithJSONPatch re-fetches the object, applies mutate, and patches
// the changed top-level status fields with an RFC 6902 JSON Patch. The first
// operation tests the resourceVersion, providing optimistic locking. On a
// resourceVersion conflict, including kube-apiserver's HTTP 422 response for a
// failed JSON Patch test operation, the whole cycle is retried automatically.
//
// JSON Patch is useful for status fields that are both required and nullable:
// unlike JSON Merge Patch, a JSON Patch operation with a null value preserves
// the field instead of interpreting null as a request to remove it.
// mutate must only modify status fields on obj.
func PatchStatusWithJSONPatch(ctx context.Context, c client.Client, obj client.Object, mutate func() error) error {
	err := retry.OnError(retry.DefaultBackoff, isJSONPatchRetryable, func() error {
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

		patch, err := buildJSONPatch(original, obj)
		if err != nil {
			return err
		}
		patchErr := c.Status().Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch))
		// kube-apiserver reports a failed JSON Patch "test" as 422/Invalid and
		// does not identify the failed operation in the returned StatusError.
		// Limit retries to errors from this patch call; the retry remains bounded.
		if apierrors.IsConflict(patchErr) || apierrors.IsInvalid(patchErr) {
			return &jsonPatchRetryError{err: patchErr}
		}
		return patchErr
	})
	var retryErr *jsonPatchRetryError
	if errors.As(err, &retryErr) {
		return retryErr.err
	}
	return err
}

func isJSONPatchRetryable(err error) bool {
	var retryErr *jsonPatchRetryError
	return errors.As(err, &retryErr)
}

type jsonPatchRetryError struct {
	err error
}

func (e *jsonPatchRetryError) Error() string {
	return e.err.Error()
}

func (e *jsonPatchRetryError) Unwrap() error {
	return e.err
}

type jsonPatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

func buildJSONPatch(original, modified client.Object) ([]byte, error) {
	originalStatus, originalStatusExists, err := statusFields(original)
	if err != nil {
		return nil, err
	}
	modifiedStatus, modifiedStatusExists, err := statusFields(modified)
	if err != nil {
		return nil, err
	}

	resourceVersion, err := json.Marshal(original.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	operations := []jsonPatchOperation{{
		Op:    "test",
		Path:  "/metadata/resourceVersion",
		Value: resourceVersion,
	}}
	if (!originalStatusExists && modifiedStatusExists) || (len(originalStatus) == 0 && len(modifiedStatus) > 0) {
		statusJSON, err := json.Marshal(modifiedStatus)
		if err != nil {
			return nil, err
		}
		operations = append(operations, jsonPatchOperation{
			Op:    "add",
			Path:  "/status",
			Value: statusJSON,
		})
		return json.Marshal(operations)
	}

	modifiedKeys := make([]string, 0, len(modifiedStatus))
	for key := range modifiedStatus {
		modifiedKeys = append(modifiedKeys, key)
	}
	sort.Strings(modifiedKeys)
	for _, key := range modifiedKeys {
		modifiedValue := modifiedStatus[key]
		originalValue, exists := originalStatus[key]
		if !exists {
			operations = append(operations, jsonPatchOperation{
				Op:    "add",
				Path:  "/status/" + escapeJSONPointerToken(key),
				Value: modifiedValue,
			})
			continue
		}
		if bytes.Equal(originalValue, modifiedValue) {
			continue
		}
		operations = append(operations, jsonPatchOperation{
			Op:    "replace",
			Path:  "/status/" + escapeJSONPointerToken(key),
			Value: modifiedValue,
		})
	}

	originalKeys := make([]string, 0, len(originalStatus))
	for key := range originalStatus {
		originalKeys = append(originalKeys, key)
	}
	sort.Strings(originalKeys)
	for _, key := range originalKeys {
		if _, exists := modifiedStatus[key]; exists {
			continue
		}
		operations = append(operations, jsonPatchOperation{
			Op:   "remove",
			Path: "/status/" + escapeJSONPointerToken(key),
		})
	}

	return json.Marshal(operations)
}

func statusFields(obj client.Object) (map[string]json.RawMessage, bool, error) {
	objectJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, false, err
	}

	var objectFields map[string]json.RawMessage
	if err := json.Unmarshal(objectJSON, &objectFields); err != nil {
		return nil, false, err
	}

	statusJSON, exists := objectFields["status"]
	if !exists || bytes.Equal(bytes.TrimSpace(statusJSON), []byte("null")) {
		return map[string]json.RawMessage{}, false, nil
	}

	status := map[string]json.RawMessage{}
	if err := json.Unmarshal(statusJSON, &status); err != nil {
		return nil, false, err
	}
	return status, true, nil
}

func escapeJSONPointerToken(token string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(token)
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

// SyncCondition copies a single condition by type from src to dst: if present in
// src it's set on dst, if absent it's removed from dst. Useful when a caller
// computes status on a working copy (to avoid PatchStatus's re-fetch clobbering
// in-memory mutations) and needs to carry a specific condition back into the
// live object's mutate callback.
func SyncCondition(src []metav1.Condition, dst *[]metav1.Condition, conditionType string) {
	if c := meta.FindStatusCondition(src, conditionType); c != nil {
		meta.SetStatusCondition(dst, *c)
	} else {
		meta.RemoveStatusCondition(dst, conditionType)
	}
}
