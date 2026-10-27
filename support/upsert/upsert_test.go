package upsert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var _ CreateOrUpdateProvider = &createOrUpdateProvider{}

type updateResultClient struct {
	crclient.Client
	remainingFailures int
	updateError       error
	mutateAfterUpdate bool
}

func (c *updateResultClient) Update(ctx context.Context, obj crclient.Object, opts ...crclient.UpdateOption) error {
	if c.remainingFailures > 0 {
		c.remainingFailures--
		return c.updateError
	}
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if c.mutateAfterUpdate {
		obj.SetAnnotations(map[string]string{"api-side-mutation": "not-in-request"})
	}
	return nil
}

func expectRejectedUpdate(t *testing.T, result controllerutil.OperationResult, err, expectedErr error) {
	t.Helper()
	if result != controllerutil.OperationResultNone || !errors.Is(err, expectedErr) {
		t.Fatalf("rejected update: got result %q, error %v", result, err)
	}
}

func TestCreateOrUpdate(t *testing.T) {
	client := fake.NewClientBuilder().WithRuntimeObjects(&appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{ServiceAccountName: "service-account"},
			},
		},
	}).Build()

	deployment := &appsv1.Deployment{}
	result, err := (&createOrUpdateProvider{}).CreateOrUpdate(t.Context(), client, deployment, func() error { return nil })
	if err != nil {
		t.Fatalf("CreateOrUpdate failed: %v", err)
	}
	if result != controllerutil.OperationResultNone {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultNone, result)
	}

	for _, testCase := range []struct {
		name        string
		updateError error
	}{
		{
			name:        "conflicting updates",
			updateError: apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "test", fmt.Errorf("conflict")),
		},
		{
			name:        "other rejected updates",
			updateError: apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "test", fmt.Errorf("forbidden")),
		},
	} {
		t.Run("When "+testCase.name+" exceed the threshold, it should count only successful writes", func(t *testing.T) {
			var logs bytes.Buffer
			detector := newUpdateLoopDetector()
			detector.log = zap.New(zap.WriteTo(&logs), zap.JSONEncoder())
			provider := &createOrUpdateProvider{loopDetector: detector}
			client := &updateResultClient{
				Client:            fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}).Build(),
				remainingFailures: updateLoopThreshold(&corev1.ConfigMap{}) + 2,
				updateError:       testCase.updateError,
			}
			obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
			key := crclient.ObjectKeyFromObject(obj)
			cacheKey := detector.keyFor(obj, key)
			mutate := func() error {
				obj.Data = map[string]string{"value": "changed"}
				return nil
			}

			for attempt := 0; attempt < updateLoopThreshold(obj)+2; attempt++ {
				result, err := provider.CreateOrUpdate(t.Context(), client, obj, mutate)
				expectRejectedUpdate(t, result, err, testCase.updateError)
			}
			if count := detector.updateEventCount[cacheKey]; count != 0 {
				t.Errorf("rejected updates counted as writes: %d", count)
			}
			if logs.Len() != 0 {
				t.Errorf("rejected updates logged a warning: %s", logs.String())
			}

			result, err := provider.CreateOrUpdate(t.Context(), client, obj, mutate)
			if err != nil || result != controllerutil.OperationResultUpdated {
				t.Fatalf("successful update: got result %q, error %v", result, err)
			}
			if count := detector.updateEventCount[cacheKey]; count != 1 {
				t.Errorf("expected one successful write, got %d", count)
			}
			result, err = provider.CreateOrUpdate(t.Context(), client, obj, mutate)
			if err != nil || result != controllerutil.OperationResultNone {
				t.Fatalf("no-op update: got result %q, error %v", result, err)
			}
			if !detector.hasNoOpUpdate.Has(cacheKey) {
				t.Error("no-op did not mark the object as settled")
			}
			result, err = provider.CreateOrUpdate(t.Context(), client, obj, func() error {
				obj.Data = map[string]string{"value": "changed-again"}
				return nil
			})
			if err != nil || result != controllerutil.OperationResultUpdated {
				t.Fatalf("update after no-op: got result %q, error %v", result, err)
			}
			if count := detector.updateEventCount[cacheKey]; count != 1 {
				t.Errorf("update after no-op changed the count to %d", count)
			}
			if logs.Len() != 0 {
				t.Errorf("unexpected warning after a no-op: %s", logs.String())
			}
		})
	}

	t.Run("When successful updates reach the threshold, it should log the requested change", func(t *testing.T) {
		var logs bytes.Buffer
		detector := newUpdateLoopDetector()
		detector.log = zap.New(zap.WriteTo(&logs), zap.JSONEncoder())
		provider := &createOrUpdateProvider{loopDetector: detector}
		client := &updateResultClient{
			Client:            fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}).Build(),
			mutateAfterUpdate: true,
		}
		obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
		key := crclient.ObjectKeyFromObject(obj)
		cacheKey := detector.keyFor(obj, key)

		for attempt := 1; attempt <= updateLoopThreshold(obj); attempt++ {
			value := fmt.Sprintf("value-%d", attempt)
			result, err := provider.CreateOrUpdate(t.Context(), client, obj, func() error {
				obj.Data = map[string]string{"value": value}
				return nil
			})
			if err != nil || result != controllerutil.OperationResultUpdated {
				t.Fatalf("update %d: got result %q, error %v", attempt, result, err)
			}
			if count := detector.updateEventCount[cacheKey]; count != attempt {
				t.Errorf("update %d: expected count %d, got %d", attempt, attempt, count)
			}
			if attempt < updateLoopThreshold(obj) && logs.Len() != 0 {
				t.Fatalf("warning before threshold: %s", logs.String())
			}
		}

		var warning struct {
			Message     string `json:"msg"`
			Diff        string `json:"diff"`
			UpdateCount int    `json:"updateCount"`
		}
		if err := json.Unmarshal(logs.Bytes(), &warning); err != nil {
			t.Fatalf("decode warning: %v; log: %s", err, logs.String())
		}
		if warning.Message != LoopDetectorWarningMessage || warning.UpdateCount != updateLoopThreshold(obj) {
			t.Errorf("unexpected warning: %+v", warning)
		}
		if !strings.Contains(warning.Diff, "value-9") || !strings.Contains(warning.Diff, "value-10") || strings.Contains(warning.Diff, "api-side-mutation") {
			t.Errorf("warning diff does not describe the requested change: %s", warning.Diff)
		}
	})
}
