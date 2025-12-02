package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr/testr"
	"google.golang.org/api/googleapi"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "GCP 404 error",
			err: &googleapi.Error{
				Code: 404,
			},
			expected: true,
		},
		{
			name: "GCP 400 error",
			err: &googleapi.Error{
				Code: 400,
			},
			expected: false,
		},
		{
			name:     "non-GCP error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isNotFoundError(test.err)
			if actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestReconcileGCPPrivateServiceConnectSpec(t *testing.T) {
	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
			// Testing with pre-populated values to avoid GCP API calls
			ForwardingRuleName: "test-forwarding-rule",
			NATSubnet:          "test-nat-subnet",
		},
	}

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
		Log:                    testr.New(t),
	}

	// Test with pre-populated spec fields to avoid GCP API calls
	err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), gcpPSC, hc)

	// Since ForwardingRuleName and NATSubnet are already set, this should succeed
	if err != nil {
		t.Errorf("unexpected error with pre-populated spec: %v", err)
	}

	// Verify the fields remain set
	if gcpPSC.Spec.ForwardingRuleName != "test-forwarding-rule" {
		t.Error("ForwardingRuleName should remain set")
	}
	if gcpPSC.Spec.NATSubnet != "test-nat-subnet" {
		t.Error("NATSubnet should remain set")
	}
}

func TestReconcile_NotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client: client,
		Log:    testr.New(t),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "test",
		},
	}

	result, err := r.Reconcile(context.Background(), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}
}

func TestReconcile_PausedUntil(t *testing.T) {
	// Use a dynamically computed future time so the test remains valid over time
	pausedUntil := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	// Create a hosted cluster with pause settings
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			PausedUntil: &pausedUntil,
		},
	}

	// Create a hosted control plane
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster",
		},
	}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-psc",
			Namespace:  "test-namespace",
			Finalizers: []string{"hypershift.openshift.io/gcp-private-service-connect"}, // Add finalizer so it gets past initial checks
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hcp, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
		Log:                    testr.New(t),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
	}

	result, err := r.Reconcile(context.Background(), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should requeue with a future time since reconciliation is paused
	if result.RequeueAfter <= 0 {
		t.Error("expected positive RequeueAfter duration for paused reconciliation")
	}
}

func TestConstructServiceAttachmentName(t *testing.T) {
	tests := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		expected    string
		description string
	}{
		{
			name: "When given a cluster ID it should construct valid service attachment name",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "12345678-abcd-1234-abcd-123456789012",
				},
			},
			expected:    "psc-12345678-abcd-1234-abcd-123456789012",
			description: "Should use psc- prefix with cluster ID (prefix ensures GCP naming compliance)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GCPPrivateServiceConnectReconciler{}
			result := r.constructServiceAttachmentName(tt.hc)
			if result != tt.expected {
				t.Errorf("expected %s, got %s - %s", tt.expected, result, tt.description)
			}
			if len(result) > 63 {
				t.Errorf("Service attachment name %s exceeds GCP 63 character limit (%d chars)", result, len(result))
			}
		})
	}
}
