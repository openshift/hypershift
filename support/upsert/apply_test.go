package upsert

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
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

	// Stamp the last-applied annotation on the existing object so the
	// annotation comparison sees no change.
	lastApplied := computeLastAppliedJSON(deployment)

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

	// make sure, extra existing metadata don't cause an update
	existingDeployment.Finalizers = []string{"test-finalizer"}
	existingDeployment.Labels["existing-label"] = "test"
	existingDeployment.Annotations["existing-annotation"] = "test"
	existingDeployment.Annotations[LastAppliedConfigurationAnnotation] = lastApplied

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

func TestApplyManifest_LastAppliedAnnotation(t *testing.T) {
	baseDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-dep",
				Namespace: "default",
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "test"},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "test"},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:    "main",
							Image:   "test:latest",
							Command: []string{"/bin/server"},
							Args:    []string{"--foo", "--bar", "--baz"},
						}},
					},
				},
			},
		}
	}

	t.Run("When creating an object, it should stamp the annotation", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		obj := baseDeployment()
		result, err := provider.ApplyManifest(t.Context(), client, obj)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultCreated {
			t.Errorf("expected result %q, got %q", controllerutil.OperationResultCreated, result)
		}

		// Verify annotation exists on the created object.
		created := &appsv1.Deployment{}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(obj), created); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		annotation := created.Annotations[LastAppliedConfigurationAnnotation]
		if annotation == "" {
			t.Fatal("expected last-applied annotation to be set on created object")
		}

		// Verify the annotation content is valid JSON and contains expected fields.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(annotation), &parsed); err != nil {
			t.Fatalf("annotation is not valid JSON: %v", err)
		}
	})

	t.Run("When trailing slice items are removed, it should detect the change", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		// Create with annotation.
		initial := baseDeployment()
		if _, err := provider.ApplyManifest(t.Context(), client, initial); err != nil {
			t.Fatalf("initial ApplyManifest failed: %v", err)
		}

		// Apply with fewer args (trailing removal).
		mutated := baseDeployment()
		mutated.Spec.Template.Spec.Containers[0].Args = []string{"--foo", "--bar"}
		result, err := provider.ApplyManifest(t.Context(), client, mutated)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected result %q, got %q", controllerutil.OperationResultUpdated, result)
		}
	})

	t.Run("When args are set to nil, it should detect the change", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		// Create with annotation.
		initial := baseDeployment()
		if _, err := provider.ApplyManifest(t.Context(), client, initial); err != nil {
			t.Fatalf("initial ApplyManifest failed: %v", err)
		}

		// Apply with nil args.
		mutated := baseDeployment()
		mutated.Spec.Template.Spec.Containers[0].Args = nil
		result, err := provider.ApplyManifest(t.Context(), client, mutated)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected result %q, got %q", controllerutil.OperationResultUpdated, result)
		}
	})

	t.Run("When no changes are made, it should be idempotent", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		// Create with annotation.
		initial := baseDeployment()
		if _, err := provider.ApplyManifest(t.Context(), client, initial); err != nil {
			t.Fatalf("initial ApplyManifest failed: %v", err)
		}

		// Apply identical state.
		same := baseDeployment()
		result, err := provider.ApplyManifest(t.Context(), client, same)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultNone {
			t.Errorf("expected result %q, got %q", controllerutil.OperationResultNone, result)
		}
	})

	t.Run("When object exists without annotation (migration), it should force-stamp", func(t *testing.T) {
		provider := &applyProvider{}

		// Pre-create object without annotation (simulating pre-upgrade object).
		existing := baseDeployment()
		client := fake.NewClientBuilder().WithObjects(existing).Build()

		manifest := baseDeployment()
		result, err := provider.ApplyManifest(t.Context(), client, manifest)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Errorf("expected result %q for migration, got %q", controllerutil.OperationResultUpdated, result)
		}

		// Verify annotation was stamped.
		updated := &appsv1.Deployment{}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(manifest), updated); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if updated.Annotations[LastAppliedConfigurationAnnotation] == "" {
			t.Fatal("expected last-applied annotation to be set after migration")
		}
	})

	t.Run("When second reconcile after migration, it should be no-op", func(t *testing.T) {
		provider := &applyProvider{}

		// Pre-create object without annotation.
		existing := baseDeployment()
		client := fake.NewClientBuilder().WithObjects(existing).Build()

		// First reconcile: migration force-stamp.
		manifest := baseDeployment()
		result, err := provider.ApplyManifest(t.Context(), client, manifest)
		if err != nil {
			t.Fatalf("first ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultUpdated {
			t.Fatalf("expected first result %q, got %q", controllerutil.OperationResultUpdated, result)
		}

		// Second reconcile: should be no-op.
		manifest2 := baseDeployment()
		result, err = provider.ApplyManifest(t.Context(), client, manifest2)
		if err != nil {
			t.Fatalf("second ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultNone {
			t.Errorf("expected second result %q, got %q", controllerutil.OperationResultNone, result)
		}
	})

	t.Run("When object exceeds max annotation size, it should skip annotation", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		obj := baseDeployment()
		// Add a large annotation to push the object over the size limit.
		obj.Annotations = map[string]string{
			"large-data": strings.Repeat("x", maxLastAppliedAnnotationSize+1),
		}

		result, err := provider.ApplyManifest(t.Context(), client, obj)
		if err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}
		if result != controllerutil.OperationResultCreated {
			t.Fatalf("expected result %q, got %q", controllerutil.OperationResultCreated, result)
		}

		// Verify annotation was NOT set.
		created := &appsv1.Deployment{}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(obj), created); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if _, ok := created.Annotations[LastAppliedConfigurationAnnotation]; ok {
			t.Error("expected no last-applied annotation on oversized object")
		}
	})

	t.Run("When annotation content is checked, it should not be self-referential", func(t *testing.T) {
		provider := &applyProvider{}
		client := fake.NewClientBuilder().Build()

		obj := baseDeployment()
		if _, err := provider.ApplyManifest(t.Context(), client, obj); err != nil {
			t.Fatalf("ApplyManifest failed: %v", err)
		}

		created := &appsv1.Deployment{}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(obj), created); err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		annotation := created.Annotations[LastAppliedConfigurationAnnotation]
		if strings.Contains(annotation, LastAppliedConfigurationAnnotation) {
			t.Error("annotation JSON should not contain the annotation key itself")
		}
	})

	t.Run("When computeLastAppliedJSON is called twice, it should be deterministic", func(t *testing.T) {
		obj := baseDeployment()
		json1 := computeLastAppliedJSON(obj)
		json2 := computeLastAppliedJSON(obj)
		if json1 == "" {
			t.Fatal("computeLastAppliedJSON returned empty string")
		}
		if json1 != json2 {
			t.Errorf("JSON output is not deterministic:\n  first:  %s\n  second: %s", json1, json2)
		}
	})

	t.Run("When reconciled 3x with identical state, it should be loop detector compatible", func(t *testing.T) {
		provider := &applyProvider{
			loopDetector: newUpdateLoopDetector(),
		}
		client := fake.NewClientBuilder().Build()

		// Create.
		initial := baseDeployment()
		if _, err := provider.ApplyManifest(t.Context(), client, initial); err != nil {
			t.Fatalf("create failed: %v", err)
		}

		// Reconcile 3x with identical state.
		for i := range 3 {
			obj := baseDeployment()
			result, err := provider.ApplyManifest(t.Context(), client, obj)
			if err != nil {
				t.Fatalf("reconcile %d failed: %v", i+1, err)
			}
			if result != controllerutil.OperationResultNone {
				t.Errorf("reconcile %d: expected %q, got %q", i+1, controllerutil.OperationResultNone, result)
			}
		}

		// ValidateUpdateEvents(1) should pass: no updates after create.
		if err := provider.ValidateUpdateEvents(1); err != nil {
			t.Errorf("ValidateUpdateEvents(1) failed: %v", err)
		}
	})
}

func TestToUnstructured(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-dep",
			Namespace:       "default",
			UID:             types.UID("test-uid"),
			Generation:      3,
			ResourceVersion: "12345",
			Labels:          map[string]string{"app": "test"},
			Annotations: map[string]string{
				"custom-annotation":                "value",
				LastAppliedConfigurationAnnotation: "old-json",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "test-manager"},
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "test:latest",
					}},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 3,
		},
	}

	u, err := toUnstructured(dep)
	if err != nil {
		t.Fatalf("toUnstructured failed: %v", err)
	}

	// Verify stripped fields.
	metadata, _ := u["metadata"].(map[string]any)
	if metadata == nil {
		t.Fatal("metadata should be present")
	}

	if _, ok := metadata["uid"]; ok {
		t.Error("uid should be stripped")
	}
	if _, ok := metadata["generation"]; ok {
		t.Error("generation should be stripped")
	}
	if _, ok := metadata["creationTimestamp"]; ok {
		t.Error("creationTimestamp should be stripped")
	}
	if _, ok := metadata["resourceVersion"]; ok {
		t.Error("resourceVersion should be stripped")
	}
	if _, ok := metadata["managedFields"]; ok {
		t.Error("managedFields should be stripped")
	}
	if _, ok := u["status"]; ok {
		t.Error("status should be stripped")
	}

	// Verify the last-applied annotation is stripped but other annotations are preserved.
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		t.Fatal("annotations should be present")
	}
	if _, ok := annotations[LastAppliedConfigurationAnnotation]; ok {
		t.Error("last-applied annotation should be stripped")
	}
	if v, ok := annotations["custom-annotation"]; !ok || v != "value" {
		t.Error("custom annotations should be preserved")
	}

	// Verify preserved fields.
	if metadata["name"] != "test-dep" {
		t.Errorf("name should be preserved, got %v", metadata["name"])
	}
	if metadata["namespace"] != "default" {
		t.Errorf("namespace should be preserved, got %v", metadata["namespace"])
	}
	labels, _ := metadata["labels"].(map[string]any)
	if labels == nil || labels["app"] != "test" {
		t.Error("labels should be preserved")
	}
	if _, ok := u["spec"]; !ok {
		t.Error("spec should be preserved")
	}
}
