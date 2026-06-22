package upsert

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ CreateOrUpdateProvider = &createOrUpdateProvider{}

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
}
