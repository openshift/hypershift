package gcpnodeterminationhandler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/go-logr/logr"
)

type fakeDrainer struct {
	called  bool
	timeout time.Duration
	err     error
}

func (d *fakeDrainer) Drain(_ context.Context, _ string, timeout time.Duration) error {
	d.called = true
	d.timeout = timeout
	return d.err
}

func TestIsPreempted(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		body       string
		expected   bool
		expectErr  bool
	}{
		{name: "When metadata returns TRUE, it should detect preemption", statusCode: http.StatusOK, body: "TRUE", expected: true},
		{name: "When metadata returns FALSE, it should not detect preemption", statusCode: http.StatusOK, body: "FALSE", expected: false},
		{name: "When metadata returns lowercase true, it should detect preemption", statusCode: http.StatusOK, body: "true", expected: true},
		{name: "When metadata returns an error status, it should return an error", statusCode: http.StatusInternalServerError, body: "", expectErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Metadata-Flavor"); got != "Google" {
					t.Fatalf("expected Metadata-Flavor header Google, got %q", got)
				}
				w.WriteHeader(tc.statusCode)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("failed to write response body: %v", err)
				}
			}))
			defer server.Close()

			h := &handler{
				metadataURL: server.URL,
				httpClient:  server.Client(),
			}

			preempted, err := h.isPreempted(context.Background())
			if tc.expectErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if preempted != tc.expected {
				t.Fatalf("expected %t, got %t", tc.expected, preempted)
			}
		})
	}
}

func TestHandlePreemption(t *testing.T) {
	testCases := []struct {
		name           string
		node           *corev1.Node
		drainErr       error
		expectErr      bool
		expectDrain    bool
		expectTainted  bool
		expectCordoned bool
	}{
		{
			name:           "When node is not already tainted, it should cordon, taint, and drain",
			node:           &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			expectDrain:    true,
			expectTainted:  true,
			expectCordoned: true,
		},
		{
			name: "When node is already tainted, it should still drain",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{{Key: preemptedTaintKey, Effect: corev1.TaintEffectNoSchedule}},
				},
			},
			expectDrain:    true,
			expectTainted:  true,
			expectCordoned: true,
		},
		{
			name:           "When drain fails, it should return an error after marking the node",
			node:           &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			drainErr:       errors.New("drain failed"),
			expectErr:      true,
			expectDrain:    true,
			expectTainted:  true,
			expectCordoned: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.node)
			drainer := &fakeDrainer{err: tc.drainErr}
			h := &handler{
				nodeName:     tc.node.Name,
				drainTimeout: 20 * time.Second,
				kubeClient:   client,
				drainer:      drainer,
				log:          logr.Discard(),
			}

			err := h.handlePreemption(context.Background())
			if tc.expectErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			updated, err := client.CoreV1().Nodes().Get(context.Background(), tc.node.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get updated node: %v", err)
			}
			if updated.Spec.Unschedulable != tc.expectCordoned {
				t.Fatalf("expected unschedulable %t, got %t", tc.expectCordoned, updated.Spec.Unschedulable)
			}
			if hasTaint(updated, preemptedTaintKey) != tc.expectTainted {
				t.Fatalf("expected taint presence %t", tc.expectTainted)
			}
			if drainer.called != tc.expectDrain {
				t.Fatalf("expected drain called %t, got %t", tc.expectDrain, drainer.called)
			}
		})
	}
}
