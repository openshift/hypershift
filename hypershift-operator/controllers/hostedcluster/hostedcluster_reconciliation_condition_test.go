package hostedcluster

import (
	"context"
	"errors"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestReconcile_WhenReconciliationFinishesItShouldStampObservedGenerationOnTheCondition(t *testing.T) {
	t.Parallel()

	reconcilerNow := metav1.Time{Time: time.Now().Round(time.Second)}
	const generation int64 = 7

	testCases := []struct {
		name           string
		reconcileError error
		expectedStatus metav1.ConditionStatus
		expectedReason string
		expectedMsg    string
	}{
		{
			name:           "When reconciliation succeeds, it should stamp the current generation",
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "ReconciliatonSucceeded",
			expectedMsg:    "Reconciliation completed successfully",
		},
		{
			name:           "When reconciliation fails, it should stamp the failing generation",
			reconcileError: errors.New("things went sideways"),
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "ReconciliationError",
			expectedMsg:    "things went sideways",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "example",
					Namespace:  "clusters",
					Generation: generation,
				},
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).WithStatusSubresource(hcluster).Build()
			r := &HostedClusterReconciler{
				Client:            c,
				CertRotationScale: 24 * time.Hour,
				overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
					return ctrl.Result{}, tc.reconcileError
				},
				now: func() metav1.Time { return reconcilerNow },
			}

			_, _ = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(hcluster)})

			if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), hcluster); err != nil {
				t.Fatalf("failed to get hostedcluster after reconciliation: %v", err)
			}

			condition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ReconciliationSucceeded))
			if condition == nil {
				t.Fatalf("expected %s condition to be set", hyperv1.ReconciliationSucceeded)
			}
			if condition.ObservedGeneration != generation {
				t.Fatalf("expected observed generation %d, got %d", generation, condition.ObservedGeneration)
			}
			if condition.Status != tc.expectedStatus {
				t.Fatalf("expected condition status %s, got %s", tc.expectedStatus, condition.Status)
			}
			if condition.Reason != tc.expectedReason {
				t.Fatalf("expected condition reason %s, got %s", tc.expectedReason, condition.Reason)
			}
			if condition.Message != tc.expectedMsg {
				t.Fatalf("expected condition message %q, got %q", tc.expectedMsg, condition.Message)
			}
		})
	}
}

func TestReconcile_WhenQueuedRequestsAreStaleItShouldReadTheLatestHostedClusterGeneration(t *testing.T) {
	t.Parallel()

	const latestGeneration int64 = 3
	reconcilerNow := metav1.Time{Time: time.Now().Round(time.Second)}

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "example",
			Namespace:  "clusters",
			Generation: 1,
		},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	storedHostedCluster := &hyperv1.HostedCluster{}
	if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), storedHostedCluster); err != nil {
		t.Fatalf("failed to get hostedcluster before updating generation: %v", err)
	}
	storedHostedCluster.Generation = latestGeneration
	if err := c.Update(t.Context(), storedHostedCluster); err != nil {
		t.Fatalf("failed to update hostedcluster generation before reconcile: %v", err)
	}

	var reconciledGenerations []int64
	r := &HostedClusterReconciler{
		Client:            c,
		CertRotationScale: 24 * time.Hour,
		overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
			reconciledGenerations = append(reconciledGenerations, hcluster.Generation)
			return ctrl.Result{}, nil
		},
		now: func() metav1.Time { return reconcilerNow },
	}

	request := ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(hcluster)}
	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(t.Context(), request); err != nil {
			t.Fatalf("reconcile %d failed: %v", i, err)
		}
	}

	if len(reconciledGenerations) != 2 {
		t.Fatalf("expected 2 reconciliations, got %d", len(reconciledGenerations))
	}
	for _, generation := range reconciledGenerations {
		if generation != latestGeneration {
			t.Fatalf("expected all reconciliations to read generation %d, got %v", latestGeneration, reconciledGenerations)
		}
	}

	if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), storedHostedCluster); err != nil {
		t.Fatalf("failed to get hostedcluster after reconciliation: %v", err)
	}
	condition := meta.FindStatusCondition(storedHostedCluster.Status.Conditions, string(hyperv1.ReconciliationSucceeded))
	if condition == nil {
		t.Fatalf("expected %s condition to be set", hyperv1.ReconciliationSucceeded)
	}
	if condition.ObservedGeneration != latestGeneration {
		t.Fatalf("expected observed generation %d, got %d", latestGeneration, condition.ObservedGeneration)
	}
}

func TestReconcile_WhenQueuedRequestsAreStaleAndActionableMetadataChangesItShouldReadTheLatestHostedClusterState(t *testing.T) {
	t.Parallel()

	reconcilerNow := metav1.Time{Time: time.Now().Round(time.Second)}
	const latestProviderImage = "quay.io/example/aws:v2"

	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "example",
			Namespace:  "clusters",
			Generation: 7,
			Annotations: map[string]string{
				hyperv1.ClusterAPIProviderAWSImage: "quay.io/example/aws:v1",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcluster).WithStatusSubresource(hcluster).Build()

	storedHostedCluster := &hyperv1.HostedCluster{}
	if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), storedHostedCluster); err != nil {
		t.Fatalf("failed to get hostedcluster before updating annotations: %v", err)
	}
	storedHostedCluster.Annotations[hyperv1.ClusterAPIProviderAWSImage] = latestProviderImage
	if err := c.Update(t.Context(), storedHostedCluster); err != nil {
		t.Fatalf("failed to update hostedcluster annotations before reconcile: %v", err)
	}

	var reconciledProviderImages []string
	r := &HostedClusterReconciler{
		Client:            c,
		CertRotationScale: 24 * time.Hour,
		overwriteReconcile: func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
			reconciledProviderImages = append(reconciledProviderImages, hcluster.Annotations[hyperv1.ClusterAPIProviderAWSImage])
			return ctrl.Result{}, nil
		},
		now: func() metav1.Time { return reconcilerNow },
	}

	request := ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(hcluster)}
	for i := 0; i < 2; i++ {
		if _, err := r.Reconcile(t.Context(), request); err != nil {
			t.Fatalf("reconcile %d failed: %v", i, err)
		}
	}

	if len(reconciledProviderImages) != 2 {
		t.Fatalf("expected 2 reconciliations, got %d", len(reconciledProviderImages))
	}
	for _, providerImage := range reconciledProviderImages {
		if providerImage != latestProviderImage {
			t.Fatalf("expected all reconciliations to read provider image %q, got %v", latestProviderImage, reconciledProviderImages)
		}
	}

	if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(hcluster), storedHostedCluster); err != nil {
		t.Fatalf("failed to get hostedcluster after reconciliation: %v", err)
	}
	condition := meta.FindStatusCondition(storedHostedCluster.Status.Conditions, string(hyperv1.ReconciliationSucceeded))
	if condition == nil {
		t.Fatalf("expected %s condition to be set", hyperv1.ReconciliationSucceeded)
	}
	if condition.ObservedGeneration != hcluster.Generation {
		t.Fatalf("expected observed generation %d, got %d", hcluster.Generation, condition.ObservedGeneration)
	}
}
