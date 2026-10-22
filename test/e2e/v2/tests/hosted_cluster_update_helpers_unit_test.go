//go:build e2ev2

package tests

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type hostedFeatureConflictClient struct {
	crclient.Client
	updates int
}

func (client *hostedFeatureConflictClient) Update(ctx context.Context, object crclient.Object, options ...crclient.UpdateOption) error {
	client.updates++
	if client.updates == 1 {
		return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, object.GetName(), nil)
	}
	return client.Client.Update(ctx, object, options...)
}

func TestUpdateHostedFeatureObject(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	t.Run("When an update conflicts, it should refetch the current object and retain unrelated fields", func(t *testing.T) {
		stored := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "test"}, Data: map[string]string{"keep": "original"}}
		client := &hostedFeatureConflictClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(stored).Build()}
		object := &corev1.ConfigMap{ObjectMeta: stored.ObjectMeta}
		calls := 0
		err := updateHostedFeatureObject(t.Context(), client, object, func(current *corev1.ConfigMap) {
			calls++
			current.Data["new"] = "value"
		})
		if err != nil {
			t.Fatal(err)
		}
		if client.updates != 2 || calls != 2 || object.Data["keep"] != "original" || object.Data["new"] != "value" {
			t.Fatal("conflict must retry from the fetched object and preserve unrelated data")
		}
		persisted := &corev1.ConfigMap{}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(stored), persisted); err != nil || persisted.Data["new"] != "value" {
			t.Fatalf("update was not persisted: %v", err)
		}
	})
	t.Run("When the resource is missing, it should not run the mutation", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		called := false
		err := updateHostedFeatureObject(t.Context(), client, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "test"}}, func(*corev1.ConfigMap) { called = true })
		if !apierrors.IsNotFound(err) || called {
			t.Fatal("a failed read must propagate without attempting the mutation")
		}
	})
}
