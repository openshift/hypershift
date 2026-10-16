package karpenterignition

import (
	"testing"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateConfigHashBaseline(t *testing.T) {
	nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "workers", Annotations: map[string]string{openshiftEC2NodeClassAnnotationCurrentConfigVersion: "legacy"}}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nc).Build()
	r := &KarpenterIgnitionReconciler{GuestClient: c}
	if err := r.updateConfigHashBaseline(t.Context(), nc, "v2:content"); err != nil {
		t.Fatal(err)
	}
	reloaded := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(nc), reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Annotations[configHashBaselineAnnotation] != "v2:content" {
		t.Fatal("baseline was not persisted")
	}
	if reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] != "legacy" {
		t.Fatal("migration changed the drift identity")
	}
	stale := reloaded.DeepCopy()
	if err := r.updateConfigHashBaseline(t.Context(), reloaded, "v2:new-content"); err != nil {
		t.Fatal(err)
	}
	if err := r.updateConfigHashBaseline(t.Context(), stale, "v2:stale"); err == nil {
		t.Fatal("stale writer should not overwrite the baseline")
	}
}

func TestUpdateConfigVersionAnnotation(t *testing.T) {
	nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "workers"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nc).Build()
	r := &KarpenterIgnitionReconciler{GuestClient: c}
	if err := r.updateConfigVersionAnnotation(t.Context(), nc, "new-content"); err != nil {
		t.Fatal(err)
	}
	reloaded := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(nc), reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Annotations[configHashBaselineAnnotation] != "v2:new-content" || reloaded.Annotations[openshiftEC2NodeClassAnnotationCurrentConfigVersion] != "new-content" {
		t.Fatal("identity and baseline must advance together")
	}
}
