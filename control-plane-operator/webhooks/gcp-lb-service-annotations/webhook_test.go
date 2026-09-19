package gcplbserviceannotations

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/gcputil"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func mustMarshal(t *testing.T, obj any) []byte {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal object: %v", err)
	}
	return b
}

func makeRequest(t *testing.T, svc *corev1.Service) *admissionv1.AdmissionRequest {
	t.Helper()
	return &admissionv1.AdmissionRequest{
		Kind:   metav1.GroupVersionKind{Kind: "Service"},
		Object: runtime.RawExtension{Raw: mustMarshal(t, svc)},
	}
}

func TestMutate_WhenLabelsEmptyAndAnnotationAbsent_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: ""}

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenNotLoadBalancer_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "goog-partner-solution=openshift"}

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP}}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenAnnotationAlreadyMatches_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "goog-partner-solution=openshift"}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: opts.Labels}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenAnnotationIsStale_ReplacesIt(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "env=prod,goog-partner-solution=openshift"}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"preserved":                        "value",
			gcputil.LBResourceLabelsAnnotation: "env=dev",
		}},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"replace"`))
	g.Expect(string(resp.Patch)).To(ContainSubstring(opts.Labels))
}

func TestMutate_WhenLabelsAreRemoved_RemovesAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: "env=prod"}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"remove"`))
}

func TestMutate_WhenLabelsAreRemovedAndAnnotationIsEmpty_RemovesAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: ""}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"remove"`))
}

func TestHandleMutate_WhenRequestExceedsMaximumSize_ReturnsBadRequest(t *testing.T) {
	opts := &Options{}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"padding": strings.Repeat("x", maxAdmissionReviewSize),
		}},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	review := &admissionv1.AdmissionReview{Request: makeRequest(t, svc)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mutate", bytes.NewReader(mustMarshal(t, review)))
	resp := httptest.NewRecorder()

	opts.handleMutate(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "http: request body too large") {
		t.Errorf("expected request body limit error, got %q", resp.Body.String())
	}
}

func TestMutate_WhenLoadBalancerWithNoAnnotations_InjectsAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "goog-partner-solution=isol_psn_0014m00001h31bnqaq_openshift"}

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).NotTo(BeNil())

	var ops []map[string]any
	g.Expect(json.Unmarshal(resp.Patch, &ops)).To(Succeed())
	g.Expect(ops).To(HaveLen(1))
	g.Expect(ops[0]["op"]).To(Equal("add"))
	g.Expect(ops[0]["path"]).To(Equal("/metadata/annotations"))
	annotations, ok := ops[0]["value"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(annotations[gcputil.LBResourceLabelsAnnotation]).To(Equal("goog-partner-solution=isol_psn_0014m00001h31bnqaq_openshift"))
}

func TestMutate_WhenLoadBalancerWithExistingAnnotations_InjectsAnnotationKey(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "goog-partner-solution=openshift"}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"existing-key": "existing-value"},
		},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := opts.mutate(makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).NotTo(BeNil())

	var ops []map[string]any
	g.Expect(json.Unmarshal(resp.Patch, &ops)).To(Succeed())
	g.Expect(ops).To(HaveLen(1))
	g.Expect(ops[0]["op"]).To(Equal("add"))
	// Key contains dots and slashes — verify correct escaping
	g.Expect(ops[0]["path"]).To(Equal("/metadata/annotations/cloud.google.com~1load-balancer-resource-labels"))
	g.Expect(ops[0]["value"]).To(Equal("goog-partner-solution=openshift"))
}

func TestMutate_WhenKindIsNotService_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &Options{Labels: "goog-partner-solution=openshift"}

	req := &admissionv1.AdmissionRequest{
		Kind:   metav1.GroupVersionKind{Kind: "Pod"},
		Object: runtime.RawExtension{Raw: mustMarshal(t, &corev1.Pod{})},
	}
	resp := opts.mutate(req)

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestJSONPatchEscapeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with/slash", "with~1slash"},
		{"with~tilde", "with~0tilde"},
		{"cloud.google.com/load-balancer-resource-labels", "cloud.google.com~1load-balancer-resource-labels"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := jsonPatchEscapeKey(tt.input); got != tt.expected {
				t.Errorf("jsonPatchEscapeKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
