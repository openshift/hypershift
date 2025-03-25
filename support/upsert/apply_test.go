package upsert

import (
	"context"
	"testing"
	"time"

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
	result, err := (&applyProvider{}).ApplyManifest(context.Background(), client, deployment)
	if err != nil {
		t.Fatalf("ApplyManifest failed: %v", err)
	}
	if result != controllerutil.OperationResultNone {
		t.Errorf("expected result %s, got %s", controllerutil.OperationResultNone, result)
	}
}
