package operator

import (
	"reflect"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGuestCacheByObject(t *testing.T) {
	tests := []struct {
		name           string
		object         client.Object
		ingressEnabled bool
		wantUnlabeled  bool
	}{
		{
			name:          "When a customer PDB has no managed label, it should be visible to the guest cache",
			object:        &policyv1.PodDisruptionBudget{},
			wantUnlabeled: true,
		},
		{
			name:          "When a Node has no managed label, it should remain visible to the guest cache",
			object:        &corev1.Node{},
			wantUnlabeled: true,
		},
		{
			name:          "When a Pod has no managed label, it should remain filtered from the guest cache",
			object:        &corev1.Pod{},
			wantUnlabeled: false,
		},
		{
			name:           "When ingress is enabled, it should make unlabeled IngressControllers visible",
			object:         &operatorv1.IngressController{},
			ingressEnabled: true,
			wantUnlabeled:  true,
		},
		{
			name:          "When ingress is disabled, it should keep unlabeled IngressControllers filtered",
			object:        &operatorv1.IngressController{},
			wantUnlabeled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := cacheLabelSelector()
			for object, options := range guestCacheByObject(tt.ingressEnabled) {
				if reflect.TypeOf(object) == reflect.TypeOf(tt.object) {
					selector = options.Label
					break
				}
			}
			if selector == nil {
				t.Fatal("guest cache selector must not be nil")
			}
			if got := selector.Matches(labels.Set{}); got != tt.wantUnlabeled {
				t.Fatalf("unlabeled %T visible = %t, want %t", tt.object, got, tt.wantUnlabeled)
			}
		})
	}
}
