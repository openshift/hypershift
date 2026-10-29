package karpenterignition

import (
	"strings"
	"testing"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testUpdateConfigAnnotationsMigration(t *testing.T) {
	nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "workers", Annotations: map[string]string{openshiftEC2NodeClassAnnotationCurrentConfigVersion: "legacy"}}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nc).Build()
	r := &KarpenterIgnitionReconciler{GuestClient: c}
	if err := r.updateConfigAnnotations(t.Context(), nc, "legacy", "content", openshiftEC2NodeClassAnnotationCurrentRolloutConfig); err != nil {
		t.Fatal(err)
	}
	reloaded := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(nc), reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentRolloutConfig] != "content" {
		t.Fatal("baseline was not persisted")
	}
	if reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] != "legacy" {
		t.Fatal("migration changed the drift identity")
	}
	stale := reloaded.DeepCopy()
	if err := r.updateConfigAnnotations(t.Context(), reloaded, "legacy", "new-content", openshiftEC2NodeClassAnnotationCurrentRolloutConfig); err != nil {
		t.Fatal(err)
	}
	if err := r.updateConfigAnnotations(t.Context(), stale, "legacy", "stale", openshiftEC2NodeClassAnnotationCurrentRolloutConfig); err == nil {
		t.Fatal("stale writer should not overwrite the baseline")
	}
}

func TestUpdateConfigAnnotations(t *testing.T) {
	t.Run("When CPO gains content validation, it should seed a separate baseline without changing identity", func(t *testing.T) {
		nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "workers"}}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nc).Build()
		r := &KarpenterIgnitionReconciler{GuestClient: c}
		legacyKey := strings.TrimSuffix(openshiftEC2NodeClassAnnotationCurrentRolloutConfig, "V2")
		for _, baseline := range []struct{ key, value string }{
			{legacyKey, "legacy-name-hash"},
			{openshiftEC2NodeClassAnnotationCurrentRolloutConfig, "current-content-hash"},
		} {
			if err := r.updateConfigAnnotations(t.Context(), nc, "retained-bootstrap", baseline.value, baseline.key); err != nil {
				t.Fatal(err)
			}
		}
		reloaded := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(nc), reloaded); err != nil {
			t.Fatal(err)
		}
		if reloaded.Annotations[legacyKey] != "legacy-name-hash" || reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentRolloutConfig] != "current-content-hash" || reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] != "retained-bootstrap" {
			t.Fatal("formula migration must keep distinct baselines and retain bootstrap identity")
		}
	})
	t.Run("When migrating a baseline, it should preserve identity and reject stale writes", testUpdateConfigAnnotationsMigration)
	nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "workers"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nc).Build()
	r := &KarpenterIgnitionReconciler{GuestClient: c}
	if err := r.updateConfigAnnotations(t.Context(), nc, "new-content", "rollout-content", openshiftEC2NodeClassAnnotationCurrentRolloutConfig); err != nil {
		t.Fatal(err)
	}
	reloaded := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(nc), reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentRolloutConfig] != "rollout-content" || reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] != "new-content" {
		t.Fatal("identity and baseline must advance together")
	}
}
