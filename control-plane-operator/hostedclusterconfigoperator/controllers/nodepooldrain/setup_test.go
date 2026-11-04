package nodepooldrain

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestNodePoolDrainPDBPredicate(t *testing.T) {
	p := nodePoolDrainPDBPredicate()
	base := func() *policyv1.PodDisruptionBudget {
		return &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Status: policyv1.PodDisruptionBudgetStatus{
				ObservedGeneration: 2,
				ExpectedPods:       3,
				DesiredHealthy:     2,
			},
		}
	}

	for _, tc := range []struct {
		name string
		old  client.Object
		new  client.Object
		want bool
	}{
		{name: "When PDB generation changes, it should enqueue a scan", old: base(), new: func() *policyv1.PodDisruptionBudget { p := base(); p.Generation++; return p }(), want: true},
		{name: "When observed generation changes, it should enqueue a scan", old: base(), new: func() *policyv1.PodDisruptionBudget { p := base(); p.Status.ObservedGeneration++; return p }(), want: true},
		{name: "When expected pods change, it should enqueue a scan", old: base(), new: func() *policyv1.PodDisruptionBudget { p := base(); p.Status.ExpectedPods++; return p }(), want: true},
		{name: "When desired healthy changes, it should enqueue a scan", old: base(), new: func() *policyv1.PodDisruptionBudget { p := base(); p.Status.DesiredHealthy++; return p }(), want: true},
		{name: "When unrelated PDB status changes, it should not enqueue a scan", old: base(), new: func() *policyv1.PodDisruptionBudget {
			p := base()
			p.Status.DisruptedPods = map[string]metav1.Time{"pod": metav1.Now()}
			return p
		}()},
		{name: "When update objects have unexpected types, it should enqueue a safe rescan", old: &corev1.ConfigMap{}, new: &corev1.ConfigMap{}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := p.Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new})
			if got != tc.want {
				t.Errorf("Update() = %t, want %t", got, tc.want)
			}
		})
	}

	if !p.Create(event.CreateEvent{Object: base()}) {
		t.Error("Create() = false, want true")
	}
	if !p.Delete(event.DeleteEvent{Object: base()}) {
		t.Error("Delete() = false, want true")
	}
}
