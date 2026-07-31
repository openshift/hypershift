package healthcheck

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAWSHealthCheckIdentityProviderConditionLogic tests the condition setting logic
// when KAS is not available. This validates that the function correctly sets the
// ValidAWSIdentityProvider condition to Unknown when it cannot perform validation.
func TestAWSHealthCheckIdentityProviderConditionLogic(t *testing.T) {
	testCases := []struct {
		name            string
		kasCondition    *metav1.Condition
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name:            "KAS not available - condition missing",
			kasCondition:    nil,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "KAS not available - condition False",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "KAS not available - condition Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionUnknown,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "KAS available",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "AWS EC2 client is not available",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test HCP
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{},
				},
			}

			// Add KAS condition if specified
			if tc.kasCondition != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tc.kasCondition)
			}

			err := awsHealthCheckIdentityProvider(t.Context(), hcp)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Verify the ValidAWSIdentityProvider condition was set correctly
			condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
			if condition == nil {
				t.Fatal("ValidAWSIdentityProvider condition was not set")
				return
			}

			if condition.Status != tc.expectedStatus {
				t.Errorf("Expected status %v, got %v", tc.expectedStatus, condition.Status)
			}

			if condition.Reason != tc.expectedReason {
				t.Errorf("Expected reason %v, got %v", tc.expectedReason, condition.Reason)
			}

			if tc.expectedMessage != "" && condition.Message != tc.expectedMessage {
				t.Errorf("Expected message %q, got %q", tc.expectedMessage, condition.Message)
			}

			if condition.ObservedGeneration != hcp.Generation {
				t.Errorf("Expected ObservedGeneration %v, got %v", hcp.Generation, condition.ObservedGeneration)
			}
		})
	}
}

func TestUpdateRunsAWSIdentityCheckDuringDeletion(t *testing.T) {
	now := metav1.Now()
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-hcp",
			Namespace:         "test-namespace",
			Generation:        1,
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcp).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).
		Build()

	ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))

	hcu := &HealthCheckUpdater{
		Client:             fakeClient,
		HostedControlPlane: client.ObjectKeyFromObject(hcp),
		log:                ctrl.Log.WithName("test"),
	}

	// With KAS unavailable and no EC2 client, awsHealthCheckIdentityProvider
	// sets ValidAWSIdentityProvider to Unknown and returns nil, so update()
	// itself succeeds but still patches the status.
	if err := hcu.update(ctx); err != nil {
		t.Fatalf("When HCP has a deletionTimestamp, update() should succeed when KAS is unavailable, got: %v", err)
	}

	updated := &hyperv1.HostedControlPlane{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), updated); err != nil {
		t.Fatalf("failed to get updated HCP: %v", err)
	}

	condition := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if condition == nil {
		t.Fatal("When HCP has a deletionTimestamp, it should still evaluate ValidAWSIdentityProvider, but the condition was not set")
	}

	// KAS is not available, so the check sets Unknown status
	if condition.Status != metav1.ConditionUnknown {
		t.Errorf("Expected condition status %v, got %v", metav1.ConditionUnknown, condition.Status)
	}

}
