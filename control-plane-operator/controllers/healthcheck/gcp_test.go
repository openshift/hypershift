package healthcheck

import (
	"fmt"
	"net/http"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGCPHealthCheckIdentityProviderConditionLogic(t *testing.T) {
	testCases := []struct {
		name            string
		kasCondition    *metav1.Condition
		gcpSpec         *hyperv1.GCPPlatformSpec
		expectError     bool
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name:            "KAS not available - condition missing",
			kasCondition:    nil,
			expectError:     false,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate GCP credentials while KubeAPIServer is not available",
		},
		{
			name: "KAS not available - condition False",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
			},
			expectError:     false,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate GCP credentials while KubeAPIServer is not available",
		},
		{
			name: "KAS available but GCP spec missing",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			gcpSpec:         nil,
			expectError:     true,
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "MissingGCPConfiguration",
			expectedMessage: "GCP platform configuration is missing from HostedControlPlane spec",
		},
		{
			name: "KAS available with GCP spec but no credentials",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			gcpSpec: &hyperv1.GCPPlatformSpec{
				Project: "test-project",
				Region:  "us-central1",
			},
			expectError:    false,
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: hyperv1.StatusUnknownReason,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-namespace",
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP:  tc.gcpSpec,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{},
				},
			}

			if tc.kasCondition != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tc.kasCondition)
			}

			err := gcpHealthCheckIdentityProvider(t.Context(), hcp)
			if tc.expectError && err == nil {
				t.Fatal("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("expected no error but got: %v", err)
			}

			for _, condType := range []string{
				string(hyperv1.ValidGCPWorkloadIdentity),
				string(hyperv1.ValidGCPCredentials),
			} {
				condition := meta.FindStatusCondition(hcp.Status.Conditions, condType)
				if condition == nil {
					t.Fatalf("%s condition was not set", condType)
				}
				if condition.Status != tc.expectedStatus {
					t.Errorf("%s: expected status %v, got %v", condType, tc.expectedStatus, condition.Status)
				}
				if condition.Reason != tc.expectedReason {
					t.Errorf("%s: expected reason %v, got %v", condType, tc.expectedReason, condition.Reason)
				}
				if tc.expectedMessage != "" && condition.Message != tc.expectedMessage {
					t.Errorf("%s: expected message %q, got %q", condType, tc.expectedMessage, condition.Message)
				}
				if condition.ObservedGeneration != hcp.Generation {
					t.Errorf("%s: expected ObservedGeneration %v, got %v", condType, hcp.Generation, condition.ObservedGeneration)
				}
			}
		})
	}
}

func TestUpdateRunsGCPIdentityCheckDuringDeletion(t *testing.T) {
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
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "test-project",
					Region:  "us-central1",
				},
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

	if err := hcu.update(ctx); err != nil {
		t.Fatalf("update() should succeed when GCP credentials are not available, got: %v", err)
	}

	updated := &hyperv1.HostedControlPlane{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), updated); err != nil {
		t.Fatalf("failed to get updated HCP: %v", err)
	}

	for _, condType := range []string{
		string(hyperv1.ValidGCPWorkloadIdentity),
		string(hyperv1.ValidGCPCredentials),
	} {
		condition := meta.FindStatusCondition(updated.Status.Conditions, condType)
		if condition == nil {
			t.Fatalf("%s condition was not set during deletion", condType)
		}
		if condition.Status != metav1.ConditionUnknown {
			t.Errorf("%s: expected status %v, got %v", condType, metav1.ConditionUnknown, condition.Status)
		}
	}
}

func TestIsPermanentGCPCredentialError(t *testing.T) {
	testCases := []struct {
		name      string
		err       error
		permanent bool
	}{
		{
			name:      "401 googleapi error is permanent",
			err:       &googleapi.Error{Code: 401, Message: "Unauthorized"},
			permanent: true,
		},
		{
			name:      "403 googleapi error is not permanent (authorization, not authentication)",
			err:       &googleapi.Error{Code: 403, Message: "Forbidden"},
			permanent: false,
		},
		{
			name: "403 googleapi error with rateLimitExceeded is not permanent",
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}},
			},
			permanent: false,
		},
		{
			name: "403 googleapi error with quotaExceeded is not permanent",
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}},
			},
			permanent: false,
		},
		{
			name: "403 googleapi error with accessNotConfigured is not permanent",
			err: &googleapi.Error{
				Code:   403,
				Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}},
			},
			permanent: false,
		},
		{
			name:      "500 googleapi error is not permanent",
			err:       &googleapi.Error{Code: 500, Message: "Internal Server Error"},
			permanent: false,
		},
		{
			name: "oauth2 RetrieveError with 401 is permanent",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: 401},
			},
			permanent: true,
		},
		{
			name: "oauth2 RetrieveError with 400 is permanent (bad WIF config)",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: 400},
			},
			permanent: true,
		},
		{
			name: "oauth2 RetrieveError with 403 is permanent (WIF pool deleted)",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: 403},
			},
			permanent: true,
		},
		{
			name: "oauth2 RetrieveError with 429 is not permanent",
			err: &oauth2.RetrieveError{
				Response: &http.Response{StatusCode: 429},
			},
			permanent: false,
		},
		{
			name:      "wrapped googleapi 401 error is permanent",
			err:       fmt.Errorf("compute call failed: %w", &googleapi.Error{Code: 401}),
			permanent: true,
		},
		{
			name:      "generic error is not permanent",
			err:       fmt.Errorf("network timeout"),
			permanent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isPermanentGCPCredentialError(tc.err)
			if got != tc.permanent {
				t.Errorf("isPermanentGCPCredentialError() = %v, want %v", got, tc.permanent)
			}
		})
	}
}
