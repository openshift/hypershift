package gcplbserviceannotations

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/gcputil"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func mustMarshal(t *testing.T, obj any) []byte {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal object: %v", err)
	}
	return b
}

func makeRequest(t *testing.T, svc *corev1.Service) admission.Request {
	t.Helper()
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:    "test-uid",
		Kind:   metav1.GroupVersionKind{Kind: "Service"},
		Object: runtime.RawExtension{Raw: mustMarshal(t, svc)},
	}}
}

func handleRequest(t *testing.T, handler *serviceAnnotationHandler, req admission.Request) admission.Response {
	t.Helper()
	response := handler.Handle(t.Context(), req)
	if err := response.Complete(req); err != nil {
		t.Fatalf("complete admission response: %v", err)
	}
	return response
}

func TestMutate_WhenLabelsEmptyAndAnnotationAbsent_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	handler := newServiceAnnotationHandler("")

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenNotLoadBalancer_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	handler := newServiceAnnotationHandler("goog-partner-solution=openshift")

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP}}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenAnnotationAlreadyMatches_DoesNotPatch(t *testing.T) {
	g := NewGomegaWithT(t)
	labels := "goog-partner-solution=openshift"
	handler := newServiceAnnotationHandler(labels)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: labels}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutate_WhenAnnotationIsStale_ReplacesIt(t *testing.T) {
	g := NewGomegaWithT(t)
	labels := "env=prod,goog-partner-solution=openshift"
	handler := newServiceAnnotationHandler(labels)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"preserved":                        "value",
			gcputil.LBResourceLabelsAnnotation: "env=dev",
		}},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"replace"`))
	g.Expect(string(resp.Patch)).To(ContainSubstring(labels))
}

func TestMutate_WhenLabelsAreRemoved_RemovesAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	handler := newServiceAnnotationHandler("")

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: "env=prod"}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"remove"`))
}

func TestMutate_WhenLabelsAreRemovedAndAnnotationIsEmpty_RemovesAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	handler := newServiceAnnotationHandler("")

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: ""}},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(string(resp.Patch)).To(ContainSubstring(`"op":"remove"`))
}

func TestHandleMutate_WhenRequestExceedsMaximumSize_ReturnsBadRequest(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"padding": strings.Repeat("x", 7*1024*1024),
		}},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	admissionRequest := makeRequest(t, svc)
	review := &admissionv1.AdmissionReview{Request: &admissionRequest.AdmissionRequest}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mutate", bytes.NewReader(mustMarshal(t, review)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	(&admission.Webhook{Handler: newServiceAnnotationHandler("")}).ServeHTTP(resp, req)

	g := NewGomegaWithT(t)
	g.Expect(resp.Code).To(Equal(http.StatusOK))
	responseReview := &admissionv1.AdmissionReview{}
	g.Expect(json.Unmarshal(resp.Body.Bytes(), responseReview)).To(Succeed())
	g.Expect(responseReview.Response.Allowed).To(BeFalse())
	g.Expect(responseReview.Response.Result.Code).To(Equal(int32(http.StatusRequestEntityTooLarge)))
}

func TestAdmissionWebhook(t *testing.T) {
	t.Run("When an AdmissionReview has no request, it should deny the review without panicking", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mutate", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()

		(&admission.Webhook{Handler: newServiceAnnotationHandler("")}).ServeHTTP(resp, req)

		g := NewGomegaWithT(t)
		g.Expect(resp.Code).To(Equal(http.StatusOK))
		responseReview := &admissionv1.AdmissionReview{}
		g.Expect(json.Unmarshal(resp.Body.Bytes(), responseReview)).To(Succeed())
		g.Expect(responseReview.Response.Allowed).To(BeFalse())
		g.Expect(responseReview.Response.Result.Code).To(Equal(int32(http.StatusBadRequest)))
	})
}

func TestNewHealthHandler(t *testing.T) {
	tests := []struct {
		name           string
		admissionReady bool
		path           string
		expectedStatus int
	}{
		{
			name:           "When the admission listener is not ready, it should report liveness",
			path:           "/healthz",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "When the admission listener is not ready, it should report readiness as unavailable",
			path:           "/readyz",
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "When the admission listener is ready, it should report readiness",
			admissionReady: true,
			path:           "/readyz",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admissionReady := &atomic.Bool{}
			admissionReady.Store(tt.admissionReady)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			newHealthHandler(admissionReady).ServeHTTP(response, request)

			if response.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, response.Code)
			}
		})
	}
}

func TestMutate_WhenLoadBalancerWithNoAnnotations_InjectsAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)
	labels := "goog-partner-solution=isol_psn_0014m00001h31bnqaq_openshift"
	handler := newServiceAnnotationHandler(labels)

	svc := &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	resp := handleRequest(t, handler, makeRequest(t, svc))

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).NotTo(BeNil())

	var ops []map[string]any
	g.Expect(json.Unmarshal(resp.Patch, &ops)).To(Succeed())
	g.Expect(ops).To(HaveLen(1))
	g.Expect(ops[0]["op"]).To(Equal("add"))
	g.Expect(ops[0]["path"]).To(Equal("/metadata/annotations"))
	annotations, ok := ops[0]["value"].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(annotations[gcputil.LBResourceLabelsAnnotation]).To(Equal(labels))
}

func TestMutate_WhenLoadBalancerWithExistingAnnotations_InjectsAnnotationKey(t *testing.T) {
	g := NewGomegaWithT(t)
	handler := newServiceAnnotationHandler("goog-partner-solution=openshift")

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"existing-key": "existing-value"},
		},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	resp := handleRequest(t, handler, makeRequest(t, svc))

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
	handler := newServiceAnnotationHandler("goog-partner-solution=openshift")

	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID:    "test-uid",
		Kind:   metav1.GroupVersionKind{Kind: "Pod"},
		Object: runtime.RawExtension{Raw: mustMarshal(t, &corev1.Pod{})},
	}}
	resp := handleRequest(t, handler, req)

	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}
