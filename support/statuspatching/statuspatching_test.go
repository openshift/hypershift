package statuspatching

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

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

type testStatusObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            testStatus `json:"status,omitempty"`
}

type testStatus struct {
	Phase string `json:"phase,omitempty"`
}

func (o *testStatusObject) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	copy := *o
	copy.ObjectMeta = *o.ObjectMeta.DeepCopy()
	return &copy
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(hyperv1.AddToScheme(s))
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

func newFakeClientWithConcurrentNodeStatusUpdateThenSuccess(scheme *runtime.Scheme, firstErr error, node *corev1.Node) (client.Client, *patchRecorder) {
	recorder := &patchRecorder{}
	attempts := 0

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(node).
		WithStatusSubresource(node).
		Build()

	intercepted := interceptor.NewClient(underlying, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			attempts++
			if attempts == 1 {
				current := &corev1.Node{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
					return err
				}
				current.Status.Conditions = append(current.Status.Conditions, corev1.NodeCondition{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				})
				if err := c.Status().Update(ctx, current); err != nil {
					return err
				}
				return firstErr
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

func newFakeClientWithPersistentPatchError(scheme *runtime.Scheme, patchErr error, objs ...client.Object) (client.Client, *int) {
	attempts := 0

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(objs...).
		Build()

	intercepted := interceptor.NewClient(underlying, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			attempts++
			return patchErr
		},
	})
	return intercepted, &attempts
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
// TEST: PatchStatusWithJSONPatch
//==================================================================

func TestPatchStatusWithJSONPatch(t *testing.T) {
	tests := []struct {
		name              string
		mutate            func(node *corev1.Node) error
		expectPatchCalled bool
	}{
		{
			name:              "When mutate makes no change, it should skip the API call",
			mutate:            func(node *corev1.Node) error { return nil },
			expectPatchCalled: false,
		},
		{
			name: "When mutate changes status, it should use JSON Patch with optimistic locking",
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
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-node",
					ResourceVersion: "100",
				},
				Status: corev1.NodeStatus{
					Phase: corev1.NodeRunning,
				},
			}
			c, recorder := newFakeClientRecorder(scheme, node)

			err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
				return tt.mutate(node)
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(recorder.called).To(Equal(tt.expectPatchCalled))

			if !tt.expectPatchCalled {
				return
			}

			g.Expect(recorder.patchType).To(Equal(types.JSONPatchType))
			var operations []jsonPatchOperation
			g.Expect(json.Unmarshal(recorder.patchData, &operations)).To(Succeed())
			g.Expect(operations).To(HaveLen(2))
			g.Expect(operations[0]).To(Equal(jsonPatchOperation{
				Op:    "test",
				Path:  "/metadata/resourceVersion",
				Value: json.RawMessage(`"100"`),
			}))
			g.Expect(operations[1].Op).To(Equal("replace"))
			g.Expect(operations[1].Path).To(Equal("/status/phase"))
		})
	}
}

func TestPatchStatusWithJSONPatch_MutateError(t *testing.T) {
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

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		return fmt.Errorf("mutate failed")
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("mutate failed"))
	g.Expect(recorder.called).To(BeFalse())
}

func TestPatchStatusWithJSONPatch_DoesNotRetryInvalidMutationError(t *testing.T) {
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
	mutateCalls := 0

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		mutateCalls++
		return apierrors.NewInvalid(schema.GroupKind{Group: "", Kind: "Node"}, node.Name, nil)
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(apierrors.IsInvalid(err)).To(BeTrue())
	g.Expect(mutateCalls).To(Equal(1))
	g.Expect(recorder.called).To(BeFalse())
}

func TestPatchStatusWithJSONPatch_RetryOnConflict(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node",
			ResourceVersion: "100",
		},
		Status: corev1.NodeStatus{Phase: corev1.NodeRunning},
	}
	c, recorder := newFakeClientWithConcurrentNodeStatusUpdateThenSuccess(scheme, apierrors.NewConflict(
		schema.GroupResource{Group: "", Resource: "nodes"},
		node.Name,
		fmt.Errorf("the object has been modified"),
	), node)

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())
	g.Expect(recorder.patchType).To(Equal(types.JSONPatchType))

	result := &corev1.Node{}
	err = c.Get(context.Background(), client.ObjectKeyFromObject(node), result)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.Status.Phase).To(Equal(corev1.NodeTerminated))
	g.Expect(result.Status.Conditions).To(HaveLen(1))
	g.Expect(result.Status.Conditions[0].Type).To(Equal(corev1.NodeReady))
}

func TestPatchStatusWithJSONPatch_RetryOnJSONPatchTestFailure(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node",
			ResourceVersion: "100",
		},
		Status: corev1.NodeStatus{Phase: corev1.NodeRunning},
	}
	c, recorder := newFakeClientWithConcurrentNodeStatusUpdateThenSuccess(scheme, apierrors.NewGenericServerResponse(
		http.StatusUnprocessableEntity,
		"",
		schema.GroupResource{},
		"",
		"testing value /metadata/resourceVersion failed: test failed",
		0,
		false,
	), node)

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())
	g.Expect(recorder.patchType).To(Equal(types.JSONPatchType))

	result := &corev1.Node{}
	g.Expect(c.Get(context.Background(), client.ObjectKeyFromObject(node), result)).To(Succeed())
	g.Expect(result.Status.Phase).To(Equal(corev1.NodeTerminated))
	g.Expect(result.Status.Conditions).To(HaveLen(1))
	g.Expect(result.Status.Conditions[0].Type).To(Equal(corev1.NodeReady))
}

func TestPatchStatusWithJSONPatch_ExhaustedRetryReturnsOriginalError(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-node",
			ResourceVersion: "100",
		},
		Status: corev1.NodeStatus{Phase: corev1.NodeRunning},
	}
	conflict := apierrors.NewConflict(
		schema.GroupResource{Group: "", Resource: "nodes"},
		node.Name,
		fmt.Errorf("the object has been modified"),
	)
	c, attempts := newFakeClientWithPersistentPatchError(scheme, conflict, node)

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(apierrors.IsConflict(err)).To(BeTrue())
	_, isRetryError := err.(*jsonPatchRetryError)
	g.Expect(isRetryError).To(BeFalse())
	g.Expect(*attempts).To(BeNumerically(">", 1))
}

func TestPatchStatusWithJSONPatch_GetFailure(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "missing-node",
			ResourceVersion: "100",
		},
	}
	c, recorder := newFakeClientRecorder(scheme)

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		node.Status.Phase = corev1.NodeTerminated
		return nil
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	g.Expect(recorder.called).To(BeFalse())
}

func TestPatchStatusWithJSONPatch_RemovesOmittedStatusField(t *testing.T) {
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

	err := PatchStatusWithJSONPatch(context.Background(), c, node, func() error {
		node.Status.Phase = ""
		return nil
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())

	var operations []jsonPatchOperation
	g.Expect(json.Unmarshal(recorder.patchData, &operations)).To(Succeed())
	g.Expect(operations).To(HaveLen(2))
	g.Expect(operations[1]).To(Equal(jsonPatchOperation{
		Op:   "remove",
		Path: "/status/phase",
	}))
}

func TestBuildJSONPatch(t *testing.T) {
	tests := []struct {
		name          string
		originalValue interface{}
		modifiedValue interface{}
		expectedOp    string
		includeOther  bool
	}{
		{
			name:          "When a status key contains slash and tilde and is added, it should escape both characters",
			originalValue: nil,
			modifiedValue: "new",
			expectedOp:    "add",
			includeOther:  true,
		},
		{
			name:          "When a status key contains slash and tilde and is replaced, it should escape both characters",
			originalValue: "old",
			modifiedValue: "new",
			expectedOp:    "replace",
		},
		{
			name:          "When a status key contains slash and tilde and is removed, it should escape both characters",
			originalValue: "old",
			modifiedValue: nil,
			expectedOp:    "remove",
			includeOther:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			originalStatus := map[string]interface{}{}
			if tt.originalValue != nil {
				originalStatus["a/b~c"] = tt.originalValue
			}
			modifiedStatus := map[string]interface{}{}
			if tt.modifiedValue != nil {
				modifiedStatus["a/b~c"] = tt.modifiedValue
			}
			if tt.includeOther {
				originalStatus["other"] = "unchanged"
				modifiedStatus["other"] = "unchanged"
			}
			original := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "example.dev/v1",
				"kind":       "Example",
				"metadata": map[string]interface{}{
					"name":            "example",
					"resourceVersion": "100",
				},
				"status": originalStatus,
			}}
			modified := original.DeepCopy()
			modified.Object["status"] = modifiedStatus

			patch, err := buildJSONPatch(original, modified)
			g.Expect(err).ToNot(HaveOccurred())
			var operations []jsonPatchOperation
			g.Expect(json.Unmarshal(patch, &operations)).To(Succeed())
			g.Expect(operations).To(HaveLen(2))
			g.Expect(operations[1].Op).To(Equal(tt.expectedOp))
			g.Expect(operations[1].Path).To(Equal("/status/a~1b~0c"))
		})
	}
}

func TestBuildJSONPatchAddsMissingStatus(t *testing.T) {
	g := NewWithT(t)
	original := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "example.dev/v1",
		"kind":       "Example",
		"metadata": map[string]interface{}{
			"name":            "example",
			"resourceVersion": "100",
		},
	}}
	modified := original.DeepCopy()
	modified.Object["status"] = map[string]interface{}{"phase": "Ready"}

	patch, err := buildJSONPatch(original, modified)
	g.Expect(err).ToNot(HaveOccurred())
	var operations []jsonPatchOperation
	g.Expect(json.Unmarshal(patch, &operations)).To(Succeed())
	g.Expect(operations).To(HaveLen(2))
	g.Expect(operations[1].Op).To(Equal("add"))
	g.Expect(operations[1].Path).To(Equal("/status"))
	g.Expect(operations[1].Value).To(MatchJSON(`{"phase":"Ready"}`))
}

func TestBuildJSONPatchAddsWholeStatusForEmptyTypedStatus(t *testing.T) {
	g := NewWithT(t)
	original := &testStatusObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-object",
			ResourceVersion: "100",
		},
	}
	modified := original.DeepCopyObject().(*testStatusObject)
	modified.Status.Phase = "Ready"

	patch, err := buildJSONPatch(original, modified)
	g.Expect(err).ToNot(HaveOccurred())
	var operations []jsonPatchOperation
	g.Expect(json.Unmarshal(patch, &operations)).To(Succeed())
	g.Expect(operations).To(HaveLen(2))
	g.Expect(operations[1].Op).To(Equal("add"))
	g.Expect(operations[1].Path).To(Equal("/status"))
	g.Expect(operations[1].Value).To(MatchJSON(`{"phase":"Ready"}`))
}

func TestBuildJSONPatchReplacesNullStatus(t *testing.T) {
	g := NewWithT(t)
	original := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "example.dev/v1",
		"kind":       "Example",
		"metadata": map[string]interface{}{
			"name":            "example",
			"resourceVersion": "100",
		},
		"status": nil,
	}}
	modified := original.DeepCopy()
	modified.Object["status"] = map[string]interface{}{"phase": "Ready"}

	patch, err := buildJSONPatch(original, modified)
	g.Expect(err).ToNot(HaveOccurred())
	var operations []jsonPatchOperation
	g.Expect(json.Unmarshal(patch, &operations)).To(Succeed())
	g.Expect(operations).To(HaveLen(2))
	g.Expect(operations[1].Op).To(Equal("add"))
	g.Expect(operations[1].Path).To(Equal("/status"))
	g.Expect(operations[1].Value).To(MatchJSON(`{"phase":"Ready"}`))
}

func TestPatchStatusWithJSONPatch_NullableFields(t *testing.T) {
	g := NewWithT(t)
	scheme := testScheme()
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-hcp",
			Namespace:       "default",
			ResourceVersion: "100",
		},
	}
	c, recorder := newFakeClientRecorder(scheme, hcp)

	err := PatchStatusWithJSONPatch(context.Background(), c, hcp, func() error {
		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{
				Version: "4.17.0",
				Image:   "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
			},
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.NewTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
					Version:     "4.17.0",
					Image:       "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
					// A partial update has no completion time yet.
					CompletionTime: nil,
				},
			},
			// A nil slice is meaningful and serializes as JSON null.
			AvailableUpdates: nil,
		}
		return nil
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(recorder.called).To(BeTrue())
	g.Expect(recorder.patchType).To(Equal(types.JSONPatchType))

	var operations []jsonPatchOperation
	g.Expect(json.Unmarshal(recorder.patchData, &operations)).To(Succeed())
	var versionStatus json.RawMessage
	for _, operation := range operations {
		if operation.Path == "/status/versionStatus" {
			versionStatus = operation.Value
			break
		}
	}
	g.Expect(versionStatus).NotTo(BeEmpty())

	var statusFields map[string]json.RawMessage
	g.Expect(json.Unmarshal(versionStatus, &statusFields)).To(Succeed())
	g.Expect(statusFields["availableUpdates"]).To(MatchJSON("null"))

	var history []map[string]json.RawMessage
	g.Expect(json.Unmarshal(statusFields["history"], &history)).To(Succeed())
	g.Expect(history).To(HaveLen(1))
	g.Expect(history[0]["completionTime"]).To(MatchJSON("null"))

	updated := &hyperv1.HostedControlPlane{}
	g.Expect(c.Get(context.Background(), client.ObjectKeyFromObject(hcp), updated)).To(Succeed())
	g.Expect(updated.Status.VersionStatus.AvailableUpdates).To(BeNil())
	g.Expect(updated.Status.VersionStatus.History[0].CompletionTime).To(BeNil())
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

func TestSyncCondition(t *testing.T) {
	tests := []struct {
		name string
		src  []metav1.Condition
		dst  []metav1.Condition
		want []metav1.Condition
	}{
		{
			name: "When the condition is present in src, it should be set on dst",
			src: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
			dst: nil,
			want: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
		},
		{
			name: "When the condition is present in both src and dst, it should overwrite dst",
			src: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
			dst: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotReady"},
			},
			want: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
		},
		{
			name: "When the condition is absent from src, it should be removed from dst",
			src:  nil,
			dst: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
			want: []metav1.Condition{},
		},
		{
			name: "When the condition is absent from both src and dst, it should leave dst unchanged",
			src:  nil,
			dst:  nil,
			want: nil,
		},
		{
			name: "When src has other condition types, it should leave dst's unrelated conditions untouched",
			src: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
			dst: []metav1.Condition{
				{Type: "Other", Status: metav1.ConditionFalse, Reason: "Unrelated"},
			},
			want: []metav1.Condition{
				{Type: "Other", Status: metav1.ConditionFalse, Reason: "Unrelated"},
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			dst := tc.dst
			SyncCondition(tc.src, &dst, "Ready")

			// SetStatusCondition stamps LastTransitionTime, so compare everything else.
			for i := range dst {
				dst[i].LastTransitionTime = metav1.Time{}
			}
			g.Expect(dst).To(Equal(tc.want))
		})
	}
}
