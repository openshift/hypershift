package upsert

import (
	"testing"
	"time"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestApplyManifest(t *testing.T) {
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
			Manager:   "hypershift-controlplane-manager",
			Operation: metav1.ManagedFieldsOperationUpdate,
			Time:      &metav1.Time{},
		},
	}

	// mare sure, extra existing metadata don't cause an update
	existingDeployment.Finalizers = []string{"test-finalizer"}
	existingDeployment.Labels["existing-label"] = "test"
	existingDeployment.Annotations["existing-annotation"] = "test"

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
				util.HCPRouteLabel: namespace,
				"other-label":      "keep-me",
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
				util.HCPRouteLabel: util.RemoveLabelMarker, // Mark for removal
				"other-label":      "keep-me",              // Keep this label
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
	if _, exists := updatedRoute.Labels[util.HCPRouteLabel]; exists {
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
				util.HCPRouteLabel: namespace,
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
				util.HCPRouteLabel: util.RemoveLabelMarker,
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
	if _, exists := updatedRoute.Labels[util.HCPRouteLabel]; exists {
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
				util.HCPRouteLabel: util.RemoveLabelMarker, // Mark for removal
				"other-label":      "keep-me",              // Keep this label
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
	if _, exists := createdRoute.Labels[util.HCPRouteLabel]; exists {
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
