package statuspatching

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

//==================================================================
// TEST INFRASTRUCTURE
//==================================================================

// patchRecorder captures Status().Patch() calls for assertion.
type patchRecorder struct {
	called    bool
	patchData []byte
	patchType types.PatchType
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

func newFakeClientRecorder(scheme *runtime.Scheme, objs ...client.Object) (client.Client, *patchRecorder) {
	recorder := &patchRecorder{}

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(objs...).
		Build()

	intercepted := interceptor.NewClient(underlying, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			recorder.called = true
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			recorder.patchData = data
			recorder.patchType = patch.Type()
			return c.Status().Patch(ctx, obj, patch, opts...)
		},
	})
	return intercepted, recorder
}

// newFakeClientWithConflictThenSuccess returns a conflict on the first
// Status().Patch() call and succeeds on subsequent calls, verifying
// that retryOnConflict actually retries.
func newFakeClientWithConflictThenSuccess(scheme *runtime.Scheme, objs ...client.Object) (client.Client, *patchRecorder) {
	recorder := &patchRecorder{}
	attempts := 0

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(objs...).
		Build()

	intercepted := interceptor.NewClient(underlying, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			attempts++
			if attempts == 1 {
				return apierrors.NewConflict(
					schema.GroupResource{Group: "", Resource: "nodes"},
					obj.GetName(),
					fmt.Errorf("the object has been modified"),
				)
			}
			recorder.called = true
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			recorder.patchData = data
			recorder.patchType = patch.Type()
			return c.Status().Patch(ctx, obj, patch, opts...)
		},
	})
	return intercepted, recorder
}

//==================================================================
// TEST: PatchStatus
//==================================================================

func TestPatchStatus(t *testing.T) {
	tests := []struct {
		name              string
		obj               func() *corev1.Node
		mutate            func(node *corev1.Node) error
		expectPatchCalled bool
	}{
		{
			name: "When mutate makes no change it should skip the API call",
			obj: func() *corev1.Node {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "test-node",
						ResourceVersion: "100",
					},
					Status: corev1.NodeStatus{
						Phase: corev1.NodeRunning,
					},
				}
			},
			mutate:            func(node *corev1.Node) error { return nil },
			expectPatchCalled: false,
		},
		{
			name: "When mutate changes status it should call Status().Patch",
			obj: func() *corev1.Node {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "test-node",
						ResourceVersion: "100",
					},
					Status: corev1.NodeStatus{
						Phase: corev1.NodeRunning,
					},
				}
			},
			mutate: func(node *corev1.Node) error {
				node.Status.Phase = corev1.NodeTerminated
				return nil
			},
			expectPatchCalled: true,
		},
		{
			name: "When mutate changes status it should include resourceVersion in patch",
			obj: func() *corev1.Node {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "test-node",
						ResourceVersion: "42",
					},
					Status: corev1.NodeStatus{
						Phase: corev1.NodeRunning,
					},
				}
			},
			mutate: func(node *corev1.Node) error {
				node.Status.Phase = corev1.NodeTerminated
				return nil
			},
			expectPatchCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := testScheme()
			node := tt.obj()
			c, recorder := newFakeClientRecorder(scheme, node)

			err := PatchStatus(context.Background(), c, node, func() error {
				return tt.mutate(node)
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(recorder.called).To(Equal(tt.expectPatchCalled))

			if tt.expectPatchCalled {
				g.Expect(recorder.patchType).To(Equal(types.MergePatchType))
				// MergeFromWithOptimisticLock includes resourceVersion in the patch payload.
				g.Expect(string(recorder.patchData)).To(ContainSubstring("resourceVersion"))
			}
		})
	}
}

func TestPatchStatus_MutateError(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node",
			ResourceVersion: "100",
		},
		Status: corev1.NodeStatus{Phase: corev1.NodeRunning},
	}
	c, recorder := newFakeClientRecorder(scheme, node)

	err := PatchStatus(context.Background(), c, node, func() error {
		return fmt.Errorf("mutate failed")
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("mutate failed"))
	g.Expect(recorder.called).To(BeFalse())
}

func TestPatchStatus_RetryOnConflict(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node",
			ResourceVersion: "100",
		},
		Status: corev1.NodeStatus{Phase: corev1.NodeRunning},
	}
	c, recorder := newFakeClientWithConflictThenSuccess(scheme, node)

	err := PatchStatus(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())

	result := &corev1.Node{}
	err = c.Get(context.Background(), client.ObjectKeyFromObject(node), result)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.Status.Phase).To(Equal(corev1.NodeTerminated))
}

func TestPatchStatus_GetFailure(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "missing-node",
			ResourceVersion: "100",
		},
	}
	c, recorder := newFakeClientRecorder(scheme)

	err := PatchStatus(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	g.Expect(recorder.called).To(BeFalse())
}

//==================================================================
// TEST: PatchStatusCondition
//==================================================================

func TestPatchStatusCondition(t *testing.T) {
	fixedTime := metav1.NewTime(metav1.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Time)

	tests := []struct {
		name               string
		existingConditions []metav1.Condition
		newCondition       metav1.Condition
		expectPatchCalled  bool
	}{
		{
			name: "When condition is already in desired state it should skip the API call",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working as expected",
					LastTransitionTime: fixedTime,
				},
			},
			newCondition: metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				Reason:             "AllGood",
				Message:            "Everything is working as expected",
				LastTransitionTime: fixedTime,
			},
			expectPatchCalled: false,
		},
		{
			name:               "When a new condition type is added it should call patch",
			existingConditions: []metav1.Condition{},
			newCondition: metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				Reason:             "AllGood",
				Message:            "Everything is working as expected",
				LastTransitionTime: fixedTime,
			},
			expectPatchCalled: true,
		},
		{
			name: "When status flips from True to False it should trigger patch",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working as expected",
					LastTransitionTime: fixedTime,
				},
			},
			newCondition: metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "SomethingWrong",
				Message:            "A dependency is unavailable",
				LastTransitionTime: fixedTime,
			},
			expectPatchCalled: true,
		},
		{
			name: "When only reason changes it should trigger patch",
			existingConditions: []metav1.Condition{
				{
					Type:               "Progressing",
					Status:             metav1.ConditionTrue,
					Reason:             "Reconciling",
					Message:            "Working on it",
					LastTransitionTime: fixedTime,
				},
			},
			newCondition: metav1.Condition{
				Type:               "Progressing",
				Status:             metav1.ConditionTrue,
				Reason:             "WaitingForDependency",
				Message:            "Blocked on upstream",
				LastTransitionTime: fixedTime,
			},
			expectPatchCalled: true,
		},
		{
			name: "When condition matches but LastTransitionTime is zero it should still skip",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working as expected",
					LastTransitionTime: fixedTime,
				},
			},
			newCondition: metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working as expected",
			},
			expectPatchCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := testScheme()

			// Use a Service — its Status.Conditions is []metav1.Condition,
			// matching how HCP exposes conditions as a bare field.
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-svc",
					Namespace:       "default",
					ResourceVersion: "100",
				},
				Status: corev1.ServiceStatus{
					Conditions: make([]metav1.Condition, len(tt.existingConditions)),
				},
			}
			copy(svc.Status.Conditions, tt.existingConditions)

			c, recorder := newFakeClientRecorder(scheme, svc)

			// Pass &svc.Status.Conditions — same pattern as &hcp.Status.Conditions.
			err := PatchStatusCondition(context.Background(), c, svc, &svc.Status.Conditions, tt.newCondition)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(recorder.called).To(Equal(tt.expectPatchCalled))

			if tt.expectPatchCalled {
				g.Expect(string(recorder.patchData)).To(ContainSubstring("resourceVersion"))
			}
		})
	}
}

func TestPatchStatusCondition_RetryOnConflict(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-svc",
			Namespace:       "default",
			ResourceVersion: "100",
		},
		Status: corev1.ServiceStatus{
			Conditions: []metav1.Condition{},
		},
	}
	c, recorder := newFakeClientWithConflictThenSuccess(scheme, svc)

	err := PatchStatusCondition(context.Background(), c, svc, &svc.Status.Conditions, metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "AllGood",
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())
	g.Expect(string(recorder.patchData)).To(ContainSubstring("resourceVersion"))

	g.Expect(svc.Status.Conditions).To(HaveLen(1))
	g.Expect(svc.Status.Conditions[0].Type).To(Equal("Ready"))
	g.Expect(svc.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
}

func TestPatchStatusCondition_GetFailure(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "missing-svc",
			Namespace:       "default",
			ResourceVersion: "100",
		},
	}
	c, recorder := newFakeClientRecorder(scheme)

	err := PatchStatusCondition(context.Background(), c, svc, &svc.Status.Conditions, metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "AllGood",
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	g.Expect(recorder.called).To(BeFalse())
}
