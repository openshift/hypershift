package hostedcluster

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestCpcConditionChangedPredicate(t *testing.T) {
	pred := cpcConditionChangedPredicate()

	baseCPC := func() *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: "clusters-test"},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hyperv1.ControlPlaneComponentAvailable),
						Status:  metav1.ConditionTrue,
						Reason:  "AsExpected",
						Message: "All is well",
					},
					{
						Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
						Status:  metav1.ConditionTrue,
						Reason:  "AsExpected",
						Message: "Rollout complete",
					},
				},
			},
		}
	}

	updateTests := []struct {
		name     string
		old      *hyperv1.ControlPlaneComponent
		new      *hyperv1.ControlPlaneComponent
		expected bool
	}{
		{
			name:     "When no conditions change, it should filter the event",
			old:      baseCPC(),
			new:      baseCPC(),
			expected: false,
		},
		{
			name: "When Available status changes, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions[0].Status = metav1.ConditionFalse
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When Available reason changes, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions[0].Reason = "PodSchedulingFailed"
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When Available message changes, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions[0].Message = "Pod etcd-0: Unschedulable"
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When RolloutComplete status changes, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions[1].Status = metav1.ConditionFalse
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When RolloutComplete reason changes, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions[1].Reason = "PodSchedulingFailed"
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When Available condition appears, it should pass through",
			old: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions = nil
				return cpc
			}(),
			new:      baseCPC(),
			expected: true,
		},
		{
			name: "When Available condition disappears, it should pass through",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions = nil
				return cpc
			}(),
			expected: true,
		},
		{
			name: "When only an unrelated condition changes, it should filter the event",
			old:  baseCPC(),
			new: func() *hyperv1.ControlPlaneComponent {
				cpc := baseCPC()
				cpc.Status.Conditions = append(cpc.Status.Conditions, metav1.Condition{
					Type:   "SomeOtherCondition",
					Status: metav1.ConditionTrue,
					Reason: "AsExpected",
				})
				return cpc
			}(),
			expected: false,
		},
	}

	for _, tc := range updateTests {
		t.Run(tc.name, func(t *testing.T) {
			got := pred.Update(event.UpdateEvent{
				ObjectOld: tc.old,
				ObjectNew: tc.new,
			})
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}

	cpc := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: "clusters-test"},
	}

	t.Run("When a Create event fires, it should pass through", func(t *testing.T) {
		if !pred.Create(event.CreateEvent{Object: cpc}) {
			t.Error("Create event should pass through")
		}
	})

	t.Run("When a Delete event fires, it should pass through", func(t *testing.T) {
		if !pred.Delete(event.DeleteEvent{Object: cpc}) {
			t.Error("Delete event should pass through")
		}
	})
}
