package upsert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/netutil"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestApplyManifest(t *testing.T) {
	testApplyManifestRejectedWrites(t)
	testApplyManifestSuccessfulWrites(t)
	testApplyManifestMetadataWrites(t)
	testApplyManifestExisting(t)
}

func testApplyManifestRejectedWrites(t *testing.T) {
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
			provider := &applyProvider{loopDetector: detector}
			client := &updateResultClient{
				Client:            fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}).Build(),
				remainingFailures: updateLoopThreshold(&corev1.ConfigMap{}) + 2,
				updateError:       testCase.updateError,
			}
			manifest := func(value string) *corev1.ConfigMap {
				return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Data: map[string]string{"value": value}}
			}
			key := crclient.ObjectKey{Name: "test"}
			cacheKey := detector.keyFor(manifest("changed"), key)

			for attempt := 0; attempt < updateLoopThreshold(manifest("changed"))+2; attempt++ {
				result, err := provider.ApplyManifest(t.Context(), client, manifest("changed"))
				if result != controllerutil.OperationResultNone || !errors.Is(err, testCase.updateError) {
					t.Fatalf("rejected update %d: got result %q, error %v", attempt, result, err)
				}
			}
			if count := detector.updateEventCount[cacheKey]; count != 0 {
				t.Errorf("rejected updates counted as writes: %d", count)
			}
			if logs.Len() != 0 {
				t.Errorf("rejected updates logged a warning: %s", logs.String())
			}

			result, err := provider.ApplyManifest(t.Context(), client, manifest("changed"))
			if err != nil || result != controllerutil.OperationResultUpdated {
				t.Fatalf("successful update: got result %q, error %v", result, err)
			}
			if count := detector.updateEventCount[cacheKey]; count != 1 {
				t.Errorf("expected one successful write, got %d", count)
			}
			result, err = provider.ApplyManifest(t.Context(), client, manifest("changed"))
			if err != nil || result != controllerutil.OperationResultNone {
				t.Fatalf("no-op update: got result %q, error %v", result, err)
			}
			if !detector.hasNoOpUpdate.Has(cacheKey) {
				t.Error("no-op did not mark the object as settled")
			}
			result, err = provider.ApplyManifest(t.Context(), client, manifest("changed-again"))
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
}

func testApplyManifestSuccessfulWrites(t *testing.T) {
	t.Run("When successful writes reach the threshold, it should log the requested change", func(t *testing.T) {
		var logs bytes.Buffer
		detector := newUpdateLoopDetector()
		detector.log = zap.New(zap.WriteTo(&logs), zap.JSONEncoder())
		provider := &applyProvider{loopDetector: detector}
		client := &updateResultClient{
			Client:            fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}).Build(),
			mutateAfterUpdate: true,
		}
		key := crclient.ObjectKey{Name: "test"}
		cacheKey := detector.keyFor(&corev1.ConfigMap{}, key)

		for attempt := 1; attempt <= updateLoopThreshold(&corev1.ConfigMap{}); attempt++ {
			manifest := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Data:       map[string]string{"value": fmt.Sprintf("value-%d", attempt)},
			}
			result, err := provider.ApplyManifest(t.Context(), client, manifest)
			if err != nil || result != controllerutil.OperationResultUpdated {
				t.Fatalf("update %d: got result %q, error %v", attempt, result, err)
			}
			if count := detector.updateEventCount[cacheKey]; count != attempt {
				t.Errorf("update %d: expected count %d, got %d", attempt, attempt, count)
			}
			if attempt < updateLoopThreshold(manifest) && logs.Len() != 0 {
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
		if warning.Message != LoopDetectorWarningMessage || warning.UpdateCount != updateLoopThreshold(&corev1.ConfigMap{}) {
			t.Errorf("unexpected warning: %+v", warning)
		}
		if !strings.Contains(warning.Diff, "value-9") || !strings.Contains(warning.Diff, "value-10") || strings.Contains(warning.Diff, "api-side-mutation") {
			t.Errorf("warning diff does not describe the requested change: %s", warning.Diff)
		}
	})
}

func testApplyManifestMetadataWrites(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		initial       *corev1.ConfigMap
		manifest      func(int) *corev1.ConfigMap
		previousValue string
		currentValue  string
	}{
		{
			name: "annotation changes",
			initial: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: "test", Annotations: map[string]string{"requested-metadata": "annotation-0"},
			}},
			manifest: func(attempt int) *corev1.ConfigMap {
				return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: "test", Annotations: map[string]string{"requested-metadata": fmt.Sprintf("annotation-%d", attempt)},
				}}
			},
			previousValue: "annotation-9",
			currentValue:  "annotation-10",
		},
		{
			name: "label removal",
			initial: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: "test", Labels: map[string]string{"requested-metadata": "label-0"},
			}},
			manifest: func(attempt int) *corev1.ConfigMap {
				value := fmt.Sprintf("label-%d", attempt)
				if attempt == updateLoopThreshold(&corev1.ConfigMap{}) {
					value = netutil.RemoveLabelMarker
				}
				return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
					Name: "test", Labels: map[string]string{"requested-metadata": value},
				}}
			},
			previousValue: "label-9",
		},
	} {
		t.Run("When metadata-only "+testCase.name+" reach the threshold, it should log the original metadata change", func(t *testing.T) {
			var logs bytes.Buffer
			detector := newUpdateLoopDetector()
			detector.log = zap.New(zap.WriteTo(&logs), zap.JSONEncoder())
			provider := &applyProvider{loopDetector: detector}
			client := fake.NewClientBuilder().WithObjects(testCase.initial).Build()
			cacheKey := detector.keyFor(testCase.initial, crclient.ObjectKey{Name: "test"})

			for attempt := 1; attempt <= updateLoopThreshold(testCase.initial); attempt++ {
				result, err := provider.ApplyManifest(t.Context(), client, testCase.manifest(attempt))
				if err != nil || result != controllerutil.OperationResultUpdated {
					t.Fatalf("update %d: got result %q, error %v", attempt, result, err)
				}
				if attempt < updateLoopThreshold(testCase.initial) && logs.Len() != 0 {
					t.Fatalf("warning before threshold: %s", logs.String())
				}
			}
			if count := detector.updateEventCount[cacheKey]; count != updateLoopThreshold(testCase.initial) {
				t.Errorf("expected %d successful writes, got %d", updateLoopThreshold(testCase.initial), count)
			}
			var warning struct {
				Message string `json:"msg"`
				Diff    string `json:"diff"`
			}
			if err := json.Unmarshal(logs.Bytes(), &warning); err != nil {
				t.Fatalf("decode warning: %v; log: %s", err, logs.String())
			}
			if warning.Message != LoopDetectorWarningMessage || !strings.Contains(warning.Diff, testCase.previousValue) ||
				(testCase.currentValue != "" && !strings.Contains(warning.Diff, testCase.currentValue)) {
				t.Errorf("warning diff does not describe the metadata change: %s", warning.Diff)
			}
		})
	}
}

func testApplyManifestExisting(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dep",
			Labels: map[string]string{
				"app": "test-deployment",
			},
			Annotations: map[string]string{
				"test-annotation": "test",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{ServiceAccountName: "service-account"},
			},
		},
	}

	// make sure read-only metadata fields are ignored.
	existingDeployment := deployment.DeepCopy()
	existingDeployment.UID = types.UID("e4e9d7ec-3811-46c1-a59a-9fdb695f409b")
	existingDeployment.Generation = 1
	existingDeployment.CreationTimestamp = metav1.Now()
	existingDeployment.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	existingDeployment.ManagedFields = []metav1.ManagedFieldsEntry{
		{
			Manager:    "hypershift-controlplane-manager",
			Operation:  metav1.ManagedFieldsOperationUpdate,
			APIVersion: "apps/v1",
			FieldsType: "FieldsV1",
			Time:       &metav1.Time{},
		},
	}

	// mare sure, extra existing metadata don't cause an update
	existingDeployment.Finalizers = []string{"test-finalizer"}
	existingDeployment.Labels["existing-label"] = "test"
	existingDeployment.Annotations["existing-annotation"] = "test"

	// Stamp the hash on the existing object so the hash comparison is a no-op.
	hash := computeDesiredHash(deployment)
	if existingDeployment.Annotations == nil {
		existingDeployment.Annotations = make(map[string]string)
	}
	existingDeployment.Annotations[DesiredStateHashAnnotation] = hash

	// make sure unset spec fields are ignored.
	existingDeployment.Spec.ProgressDeadlineSeconds = ptr.To[int32](600)
	existingDeployment.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst

	// make sure status is ignored.
	existingDeployment.Status = appsv1.DeploymentStatus{
		ObservedGeneration: 2,
		Replicas:           1,
		UpdatedReplicas:    1,
		ReadyReplicas:      1,
		Conditions: []appsv1.DeploymentCondition{
			{
				Type:    appsv1.DeploymentAvailable,
				Status:  corev1.ConditionTrue,
				Message: "Deployment Available",
			},
		},
	}

	client := fake.NewClientBuilder().WithObjects(existingDeployment).Build()
	result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, deployment)
	if err != nil {
		t.Fatalf("ApplyManifest failed: %v", err)
	}
	if result != controllerutil.OperationResultNone {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultNone, result)
	}
}

// TestApplyManifestLabelRemoval proves that the label removal mechanism works correctly
// when using ApplyManifest with the component framework. This test demonstrates why
// the changes in apply.go are necessary:
//
//  1. preserveOriginalMetadata must process RemoveLabelMarker to remove labels
//  2. The update function must detect label removal and perform updates even when
//     DeepDerivative says objects are equal (because DeepDerivative ignores empty maps)
//
// Without these changes, labels marked for removal would not be removed from cluster
// objects when using ApplyManifest, which is needed when transitioning routes from
// HCP-managed to default ingress controller-managed.
func TestApplyManifestLabelRemoval(t *testing.T) {
	const namespace = "test-ns"
	const routeName = "test-route"

	// Existing route in cluster with HCPRouteLabel
	existingRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				netutil.HCPRouteLabel: namespace,
				"other-label":         "keep-me",
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "test-service",
			},
		},
	}

	// Manifest route that marks HCPRouteLabel for removal (using RemoveLabelMarker)
	// This simulates the scenario where we want to remove the HCP route label
	// when transitioning from HCP router to default ingress controller
	manifestRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				netutil.HCPRouteLabel: netutil.RemoveLabelMarker, // Mark for removal
				"other-label":         "keep-me",                 // Keep this label
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "test-service",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(existingRoute).
		Build()
	provider := &applyProvider{}

	// Apply the manifest - this should remove the HCPRouteLabel
	result, err := provider.ApplyManifest(t.Context(), client, manifestRoute)
	if err != nil {
		t.Fatalf("ApplyManifest failed: %v", err)
	}

	// The update should have occurred because we're removing a label
	if result != controllerutil.OperationResultUpdated {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultUpdated, result)
	}

	// Verify the label was actually removed from the cluster object
	var updatedRoute routev1.Route
	if err := client.Get(t.Context(), types.NamespacedName{Name: routeName, Namespace: namespace}, &updatedRoute); err != nil {
		t.Fatalf("failed to get updated route: %v", err)
	}

	// HCPRouteLabel should be removed
	if _, exists := updatedRoute.Labels[netutil.HCPRouteLabel]; exists {
		t.Errorf("expected HCPRouteLabel to be removed, but it still exists")
	}

	// Other labels should be preserved
	if updatedRoute.Labels["other-label"] != "keep-me" {
		t.Errorf("expected other-label to be preserved, got %v", updatedRoute.Labels["other-label"])
	}

	// Verify that only one label remains
	if len(updatedRoute.Labels) != 1 {
		t.Errorf("expected 1 label remaining, got %d: %v", len(updatedRoute.Labels), updatedRoute.Labels)
	}
}

// TestApplyManifestLabelRemovalWithEmptyLabels tests the edge case where removing
// the last label results in an empty label map. This proves that the special handling
// in update() correctly detects label removal even when DeepDerivative would normally
// say the objects are equal (because it ignores empty maps).
func TestApplyManifestLabelRemovalWithEmptyLabels(t *testing.T) {
	const namespace = "test-ns"
	const routeName = "test-route-empty"

	// Existing route with only HCPRouteLabel
	existingRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				netutil.HCPRouteLabel: namespace,
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "test-service",
			},
		},
	}

	// Manifest route that marks HCPRouteLabel for removal, resulting in empty labels
	manifestRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				netutil.HCPRouteLabel: netutil.RemoveLabelMarker,
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "test-service",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(existingRoute).
		Build()
	provider := &applyProvider{}

	// Apply the manifest - this should remove the only label, resulting in empty labels
	result, err := provider.ApplyManifest(t.Context(), client, manifestRoute)
	if err != nil {
		t.Fatalf("ApplyManifest failed: %v", err)
	}

	// The update should have occurred even though DeepDerivative would say they're equal
	// (because empty maps are ignored), but we need the update to remove the label
	if result != controllerutil.OperationResultUpdated {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultUpdated, result)
	}

	// Verify the label was actually removed
	var updatedRoute routev1.Route
	if err := client.Get(t.Context(), types.NamespacedName{Name: routeName, Namespace: namespace}, &updatedRoute); err != nil {
		t.Fatalf("failed to get updated route: %v", err)
	}

	// HCPRouteLabel should be removed
	if _, exists := updatedRoute.Labels[netutil.HCPRouteLabel]; exists {
		t.Errorf("expected HCPRouteLabel to be removed, but it still exists")
	}

	// Labels should be empty or nil
	if len(updatedRoute.Labels) != 0 {
		t.Errorf("expected empty labels, got %d labels: %v", len(updatedRoute.Labels), updatedRoute.Labels)
	}
}

// TestApplyManifestLabelRemovalOnCreate tests that removal markers are cleaned up
// when creating a new object. This is critical because Kubernetes validates label
// values during creation, and the removal marker value is not a valid label value.
// This test ensures the fix for Azure ignition server Route creation failures.
func TestApplyManifestLabelRemovalOnCreate(t *testing.T) {
	const namespace = "test-ns"
	const routeName = "test-route-new"

	// Manifest route with removal marker - simulates the Azure scenario where
	// a Route is being created with a removal marker (e.g., when transitioning
	// from HCP router to default ingress controller)
	manifestRoute := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				netutil.HCPRouteLabel: netutil.RemoveLabelMarker, // Mark for removal
				"other-label":         "keep-me",                 // Keep this label
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: "test-service",
			},
		},
	}

	// Empty client - route doesn't exist yet, so it will be created
	client := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()
	provider := &applyProvider{}

	// Apply the manifest - this should create the route with removal marker cleaned up
	result, err := provider.ApplyManifest(t.Context(), client, manifestRoute)
	if err != nil {
		t.Fatalf("ApplyManifest failed: %v", err)
	}

	// The route should have been created
	if result != controllerutil.OperationResultCreated {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultCreated, result)
	}

	// Verify the route was created and removal marker was cleaned up
	var createdRoute routev1.Route
	if err := client.Get(t.Context(), types.NamespacedName{Name: routeName, Namespace: namespace}, &createdRoute); err != nil {
		t.Fatalf("failed to get created route: %v", err)
	}

	// HCPRouteLabel should not exist (removal marker was cleaned up before creation)
	if _, exists := createdRoute.Labels[netutil.HCPRouteLabel]; exists {
		t.Errorf("expected HCPRouteLabel to be removed before creation, but it still exists")
	}

	// Other labels should be preserved
	if createdRoute.Labels["other-label"] != "keep-me" {
		t.Errorf("expected other-label to be preserved, got %v", createdRoute.Labels["other-label"])
	}

	// Verify that only one label remains
	if len(createdRoute.Labels) != 1 {
		t.Errorf("expected 1 label remaining, got %d: %v", len(createdRoute.Labels), createdRoute.Labels)
	}
}

func makeHashTestDeployment(args ...string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dep",
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "main",
						Image:   "registry.example.com/image:latest",
						Command: []string{"/usr/bin/server"},
						Args:    args,
					}},
				},
			},
		},
	}
}

func TestApplyManifest_DesiredStateHash(t *testing.T) {
	t.Run("create stamps the hash annotation", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar", "--baz")
		client := fake.NewClientBuilder().Build()
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultCreated {
			t.Fatalf("expected Created, got %s", result)
		}

		var created appsv1.Deployment
		if err := client.Get(t.Context(), types.NamespacedName{Name: "test-dep", Namespace: "test-ns"}, &created); err != nil {
			t.Fatalf("get failed: %v", err)
		}
		hash := created.Annotations[DesiredStateHashAnnotation]
		if len(hash) != 64 {
			t.Fatalf("expected 64-char hash, got %q (len=%d)", hash, len(hash))
		}
	})

	t.Run("trailing slice removal detected", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar", "--baz")
		client := fake.NewClientBuilder().Build()
		// Create
		if _, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep); err != nil {
			t.Fatal(err)
		}

		// Remove --baz
		dep2 := makeHashTestDeployment("--foo", "--bar")
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected Updated (trailing arg removed), got %s", result)
		}
	})

	t.Run("nil args detected", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar")
		client := fake.NewClientBuilder().Build()
		if _, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep); err != nil {
			t.Fatal(err)
		}

		// Set args to nil
		dep2 := makeHashTestDeployment()
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected Updated (args nil'd), got %s", result)
		}
	})

	t.Run("idempotent on identical state", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar")
		client := fake.NewClientBuilder().Build()
		if _, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep); err != nil {
			t.Fatal(err)
		}

		dep2 := makeHashTestDeployment("--foo", "--bar")
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultNone {
			t.Errorf("expected None (idempotent), got %s", result)
		}
	})

	t.Run("deterministic hash", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar", "--baz")
		h1 := computeDesiredHash(dep)
		h2 := computeDesiredHash(dep)
		if h1 != h2 {
			t.Errorf("hash is not deterministic: %s != %s", h1, h2)
		}
		if len(h1) != 64 {
			t.Errorf("expected 64-char hex hash, got len=%d: %s", len(h1), h1)
		}
	})
}

func TestApplyManifest_DesiredStateHashEdgeCases(t *testing.T) {
	t.Run("external drift detected via DeepDerivative fallback despite matching hash", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo", "--bar")
		client := fake.NewClientBuilder().Build()

		// Create - stamps the hash based on dep's desired spec.
		if _, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep); err != nil {
			t.Fatal(err)
		}

		// Simulate an external modification to the live object's spec (e.g. someone
		// running `kubectl edit`, or an unrelated controller), without touching the
		// hash annotation our controller stamped on create.
		var drifted appsv1.Deployment
		if err := client.Get(t.Context(), types.NamespacedName{Name: "test-dep", Namespace: "test-ns"}, &drifted); err != nil {
			t.Fatal(err)
		}
		drifted.Spec.Template.Spec.Containers[0].Image = "someone-else/hijacked:latest"
		if err := client.Update(t.Context(), &drifted); err != nil {
			t.Fatal(err)
		}

		// Reconcile again with the exact same desired manifest as before, so the
		// hash comparison alone would say "no update needed". The DeepDerivative
		// fallback must still catch the drift and force an update.
		dep2 := makeHashTestDeployment("--foo", "--bar")
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected Updated (external drift detected via DeepDerivative fallback), got %s", result)
		}

		// Verify the drift was actually corrected.
		var reconciled appsv1.Deployment
		if err := client.Get(t.Context(), types.NamespacedName{Name: "test-dep", Namespace: "test-ns"}, &reconciled); err != nil {
			t.Fatal(err)
		}
		if reconciled.Spec.Template.Spec.Containers[0].Image != "registry.example.com/image:latest" {
			t.Errorf("expected drifted image to be corrected, got %q", reconciled.Spec.Template.Spec.Containers[0].Image)
		}
	})

	t.Run("migration force-stamps hash on pre-existing object", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo")
		// Pre-existing object without hash annotation
		existing := dep.DeepCopy()
		existing.ResourceVersion = "1"
		client := fake.NewClientBuilder().WithObjects(existing).Build()

		dep2 := makeHashTestDeployment("--foo")
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected Updated (migration stamp), got %s", result)
		}

		// Verify hash was stamped
		var updated appsv1.Deployment
		if err := client.Get(t.Context(), types.NamespacedName{Name: "test-dep", Namespace: "test-ns"}, &updated); err != nil {
			t.Fatal(err)
		}
		if updated.Annotations[DesiredStateHashAnnotation] == "" {
			t.Error("expected hash annotation after migration, got empty")
		}
	})

	t.Run("second reconcile after migration is no-op", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo")
		existing := dep.DeepCopy()
		existing.ResourceVersion = "1"
		client := fake.NewClientBuilder().WithObjects(existing).Build()

		// First reconcile: migration stamp
		dep2 := makeHashTestDeployment("--foo")
		if _, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep2); err != nil {
			t.Fatal(err)
		}

		// Second reconcile: should be no-op
		dep3 := makeHashTestDeployment("--foo")
		result, err := (&applyProvider{}).ApplyManifest(t.Context(), client, dep3)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultNone {
			t.Errorf("expected None (post-migration no-op), got %s", result)
		}
	})

	t.Run("loop detector compatible", func(t *testing.T) {
		dep := makeHashTestDeployment("--foo")
		client := fake.NewClientBuilder().Build()
		provider := &applyProvider{loopDetector: newUpdateLoopDetector()}

		// Create
		if _, err := provider.ApplyManifest(t.Context(), client, dep); err != nil {
			t.Fatal(err)
		}

		// 3 identical reconciles
		for i := 0; i < 3; i++ {
			d := makeHashTestDeployment("--foo")
			if _, err := provider.ApplyManifest(t.Context(), client, d); err != nil {
				t.Fatal(err)
			}
		}

		if err := provider.ValidateUpdateEvents(1); err != nil {
			t.Errorf("loop detector fired on stable reconciles: %v", err)
		}
	})
}

func TestToUnstructured(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test",
			Namespace:       "ns",
			UID:             types.UID("test-uid"),
			Generation:      5,
			ResourceVersion: "42",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "test"},
			},
			Annotations: map[string]string{
				DesiredStateHashAnnotation: "abc123",
				"other-annotation":         "keep",
			},
			Labels: map[string]string{
				"app": "test",
			},
		},
		Status: appsv1.DeploymentStatus{Replicas: 3},
	}

	u, err := toUnstructured(dep)
	if err != nil {
		t.Fatal(err)
	}

	metadata := u["metadata"].(map[string]any)

	// Volatile fields should be stripped
	for _, field := range []string{"uid", "generation", "creationTimestamp", "resourceVersion", "managedFields"} {
		if _, ok := metadata[field]; ok {
			t.Errorf("expected %s to be stripped, but it's present", field)
		}
	}

	// Status should be stripped
	if _, ok := u["status"]; ok {
		t.Error("expected status to be stripped")
	}

	// Hash annotation should be stripped
	annotations := metadata["annotations"].(map[string]any)
	if _, ok := annotations[DesiredStateHashAnnotation]; ok {
		t.Error("expected hash annotation to be stripped")
	}

	// Other annotations and labels should be preserved
	if annotations["other-annotation"] != "keep" {
		t.Error("expected other-annotation to be preserved")
	}
	labels := metadata["labels"].(map[string]any)
	if labels["app"] != "test" {
		t.Error("expected app label to be preserved")
	}

	// Name should be preserved
	if metadata["name"] != "test" {
		t.Error("expected name to be preserved")
	}
}
