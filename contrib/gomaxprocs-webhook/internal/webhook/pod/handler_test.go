package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr/testr"
	intscheme "github.com/openshift/hypershift/contrib/gomaxprocs-webhook/internal/scheme"
)

type staticLoader struct {
	val     string
	exclude bool
	ok      bool
}

func (s staticLoader) Resolve(context.Context, string, string, string) (string, bool, bool) {
	return s.val, s.exclude, s.ok
}

// test helpers to reduce repetition in table-driven tests
func newNamespace(name string, labeled bool) *corev1.Namespace {
	labels := map[string]string{}
	if labeled {
		labels["hypershift.openshift.io/hosted-control-plane"] = "true"
	}
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func mustMarshalPod(t *testing.T, namespace string) []byte {
	t.Helper()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: namespace}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}}}
	b, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("failed to marshal pod: %v", err)
	}
	return b
}

func newRequest(kind metav1.GroupVersionKind, namespace string, op admissionv1.Operation, raw []byte) admission.Request {
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:       "uid",
		Kind:      kind,
		Namespace: namespace,
		Operation: op,
		Object:    runtime.RawExtension{Raw: raw},
	}}
}

func TestInjectAddsEnvWhenConfigured(t *testing.T) {
	scheme := intscheme.New()
	ns := &corev1.Namespace{}
	ns.Name = "cp"
	ns.Labels = map[string]string{"hypershift.openshift.io/hosted-control-plane": "true"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()

	h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: "3", ok: true})

	pod := &corev1.Pod{}
	pod.Namespace = ns.Name
	pod.Spec.Containers = []corev1.Container{{Name: "a"}}
	mutated := pod.DeepCopy()
	changed := h.injectForPod(context.Background(), mutated)
	if !changed {
		t.Fatalf("expected mutation")
	}
	if got := mutated.Spec.Containers[0].Env[0]; got.Name != "GOMAXPROCS" || got.Value != "3" {
		t.Fatalf("unexpected env: %#v", got)
	}
}

func TestResolveTopOwner_BasicTraversal(t *testing.T) {
	tests := []struct {
		name         string
		pod          *corev1.Pod
		objects      []client.Object
		expectedKind string
		expectedName string
	}{
		{
			name: "no owner references",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "pod-uid",
				},
			},
			objects:      []client.Object{},
			expectedKind: "",
			expectedName: "",
		},
		{
			name: "single level - StatefulSet",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "pod-uid",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "StatefulSet",
						Name: "test-statefulset",
						UID:  "ss-uid",
					}},
				},
			},
			objects:      []client.Object{},
			expectedKind: "StatefulSet",
			expectedName: "test-statefulset",
		},
		{
			name: "two levels - ReplicaSet to Deployment",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "pod-uid",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "ReplicaSet",
						Name: "test-rs",
						UID:  "rs-uid",
					}},
				},
			},
			objects: []client.Object{
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rs",
						Namespace: "default",
						UID:       "rs-uid",
						OwnerReferences: []metav1.OwnerReference{{
							Kind: "Deployment",
							Name: "test-deployment",
							UID:  "deploy-uid",
						}},
					},
				},
			},
			expectedKind: "Deployment",
			expectedName: "test-deployment",
		},
		{
			name: "two levels - Job to CronJob",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "pod-uid",
					OwnerReferences: []metav1.OwnerReference{{
						Kind: "Job",
						Name: "test-job",
						UID:  "job-uid",
					}},
				},
			},
			objects: []client.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-job",
						Namespace: "default",
						UID:       "job-uid",
						OwnerReferences: []metav1.OwnerReference{{
							Kind: "CronJob",
							Name: "test-cronjob",
							UID:  "cronjob-uid",
						}},
					},
				},
			},
			expectedKind: "CronJob",
			expectedName: "test-cronjob",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := intscheme.New()

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: "3", ok: true})

			kind, name := h.resolveTopOwner(context.Background(), tt.pod)

			if kind != tt.expectedKind {
				t.Errorf("expected kind=%s, got %s", tt.expectedKind, kind)
			}
			if name != tt.expectedName {
				t.Errorf("expected name=%s, got %s", tt.expectedName, name)
			}
		})
	}
}

func TestResolveTopOwner_DepthLimits(t *testing.T) {
	// Create a deep chain that exceeds maxOwnerTraversalDepth
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "rs-1",
				UID:  "rs-1-uid",
			}},
		},
	}

	objects := []client.Object{}

	// Create a chain: rs-1 -> rs-2 -> rs-3 -> rs-4 -> rs-5 -> rs-6 (exceeds limit of 5)
	for i := 1; i <= 6; i++ {
		rs := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("rs-%d", i),
				Namespace: "default",
				UID:       types.UID(fmt.Sprintf("rs-%d-uid", i)),
			},
		}

		if i < 6 { // Add owner reference to next in chain
			rs.OwnerReferences = []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: fmt.Sprintf("rs-%d", i+1),
				UID:  types.UID(fmt.Sprintf("rs-%d-uid", i+1)),
			}}
		}

		objects = append(objects, rs)
	}

	scheme := intscheme.New()

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: "3", ok: true})

	kind, name := h.resolveTopOwner(context.Background(), pod)

	// Should stop at the depth limit, which would be rs-5 (starting from rs-1, depth 0-4)
	expectedKind := "ReplicaSet"
	expectedName := "rs-5" // Should stop at 5th level due to maxOwnerTraversalDepth = 5

	if kind != expectedKind {
		t.Errorf("expected kind=%s, got %s", expectedKind, kind)
	}
	if name != expectedName {
		t.Errorf("expected name=%s, got %s", expectedName, name)
	}
}

func TestResolveTopOwner_CycleDetection(t *testing.T) {
	// Create a cycle: rs-1 -> rs-2 -> rs-1
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "rs-1",
				UID:  "rs-1-uid",
			}},
		},
	}

	rs1 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rs-1",
			Namespace: "default",
			UID:       "rs-1-uid",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "rs-2",
				UID:  "rs-2-uid",
			}},
		},
	}

	rs2 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rs-2",
			Namespace: "default",
			UID:       "rs-2-uid",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "rs-1", // Creates cycle back to rs-1
				UID:  "rs-1-uid",
			}},
		},
	}

	scheme := intscheme.New()

	objects := []client.Object{rs1, rs2}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: "3", ok: true})

	kind, name := h.resolveTopOwner(context.Background(), pod)

	// Should detect cycle and stop at rs-2 (last valid owner before cycle detection)
	expectedKind := "ReplicaSet"
	expectedName := "rs-2"

	if kind != expectedKind {
		t.Errorf("expected kind=%s, got %s", expectedKind, kind)
	}
	if name != expectedName {
		t.Errorf("expected name=%s, got %s", expectedName, name)
	}
}

func TestResolveTopOwner_ErrorHandling(t *testing.T) {
	// Test graceful handling when owner references point to non-existent resources
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet",
				Name: "nonexistent-rs",
				UID:  "nonexistent-uid",
			}},
		},
	}

	scheme := intscheme.New()
	_ = appsv1.AddToScheme(scheme)

	// No objects in fake client - owner reference will fail to resolve
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: "3", ok: true})

	kind, name := h.resolveTopOwner(context.Background(), pod)

	// Should gracefully handle error and return the owner reference it was trying to resolve
	expectedKind := "ReplicaSet"
	expectedName := "nonexistent-rs"

	if kind != expectedKind {
		t.Errorf("expected kind=%s, got %s", expectedKind, kind)
	}
	if name != expectedName {
		t.Errorf("expected name=%s, got %s", expectedName, name)
	}
}

func TestHandle(t *testing.T) {
	tests := []struct {
		name            string
		kind            metav1.GroupVersionKind
		op              admissionv1.Operation
		namespace       string
		nsExists        bool
		nsLabeled       bool
		rawKind         string // pod | invalid | empty
		loaderVal       string
		loaderOk        bool
		expectAllowed   bool
		expectHTTPCode  int // 0 means no specific code expected
		expectPatches   bool
		expectJSONPatch bool
	}{
		{
			name:          "When kind is not Pod it should allow and skip",
			kind:          metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
			op:            admissionv1.Create,
			namespace:     "cp",
			nsExists:      true,
			nsLabeled:     true,
			rawKind:       "empty",
			loaderVal:     "3",
			loaderOk:      true,
			expectAllowed: true,
		},
		{
			name:          "When operation is not CREATE it should allow",
			kind:          metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:            admissionv1.Update,
			namespace:     "cp",
			nsExists:      true,
			nsLabeled:     true,
			rawKind:       "pod",
			loaderVal:     "3",
			loaderOk:      true,
			expectAllowed: true,
		},
		{
			name:          "When namespace not found it should allow",
			kind:          metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:            admissionv1.Create,
			namespace:     "missing",
			nsExists:      false,
			nsLabeled:     false,
			rawKind:       "pod",
			loaderVal:     "3",
			loaderOk:      true,
			expectAllowed: true,
		},
		{
			name:          "When namespace lacks label it should allow",
			kind:          metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:            admissionv1.Create,
			namespace:     "cp",
			nsExists:      true,
			nsLabeled:     false,
			rawKind:       "pod",
			loaderVal:     "3",
			loaderOk:      true,
			expectAllowed: true,
		},
		{
			name:           "When decode fails it should reject with 400",
			kind:           metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:             admissionv1.Create,
			namespace:      "cp",
			nsExists:       true,
			nsLabeled:      true,
			rawKind:        "invalid",
			loaderVal:      "3",
			loaderOk:       true,
			expectAllowed:  false,
			expectHTTPCode: 400,
		},
		{
			name:          "When no mutation occurs it should allow with no patches",
			kind:          metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:            admissionv1.Create,
			namespace:     "cp",
			nsExists:      true,
			nsLabeled:     true,
			rawKind:       "pod",
			loaderVal:     "",
			loaderOk:      false,
			expectAllowed: true,
			expectPatches: false,
		},
		{
			name:            "When mutation occurs it should return JSONPatch",
			kind:            metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			op:              admissionv1.Create,
			namespace:       "cp",
			nsExists:        true,
			nsLabeled:       true,
			rawKind:         "pod",
			loaderVal:       "3",
			loaderOk:        true,
			expectAllowed:   true,
			expectPatches:   true,
			expectJSONPatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := intscheme.New()

			var objs []client.Object
			if tt.nsExists {
				objs = append(objs, newNamespace(tt.namespace, tt.nsLabeled))
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			h := NewHandler(testr.New(t), c, admission.NewDecoder(scheme), staticLoader{val: tt.loaderVal, ok: tt.loaderOk})

			var raw []byte
			switch tt.rawKind {
			case "invalid":
				raw = []byte("not-json")
			case "pod":
				raw = mustMarshalPod(t, tt.namespace)
			default:
				raw = []byte(`{}`)
			}

			req := newRequest(tt.kind, tt.namespace, tt.op, raw)

			resp := h.Handle(context.Background(), req)

			if tt.expectAllowed != resp.Allowed {
				t.Fatalf("expected Allowed=%v, got %v", tt.expectAllowed, resp.Allowed)
			}
			if tt.expectHTTPCode != 0 {
				if resp.Result == nil || resp.Result.Code != int32(tt.expectHTTPCode) {
					t.Fatalf("expected HTTP %d, got %+v", tt.expectHTTPCode, resp.Result)
				}
			}
			if tt.expectPatches {
				if len(resp.Patches) == 0 {
					t.Fatalf("expected patches to be present on mutation")
				}
			} else {
				if len(resp.Patches) != 0 {
					t.Fatalf("expected no patches, got %d", len(resp.Patches))
				}
			}
			if tt.expectJSONPatch {
				if resp.PatchType == nil || *resp.PatchType != admissionv1.PatchTypeJSONPatch {
					t.Fatalf("expected JSONPatch response, got %#v", resp.PatchType)
				}
			}
		})
	}
}
