package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
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
			name:            "When KAS condition is missing, it should set credentials condition to Unknown",
			kasCondition:    nil,
			expectError:     false,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate GCP credentials while KubeAPIServer is not available",
		},
		{
			name: "When KAS condition is False, it should set credentials condition to Unknown",
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
			name: "When KAS is available but GCP spec is missing, it should set credentials condition to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			gcpSpec:         nil,
			expectError:     true,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "MissingGCPConfiguration",
			expectedMessage: "GCP platform configuration is missing from HostedControlPlane spec",
		},
		{
			name: "When KAS is available with GCP spec but no credentials, it should set credentials condition to Unknown",
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

func TestGCPHealthCheckConditionDifferentiation(t *testing.T) {
	kasTrue := &metav1.Condition{
		Type:   string(hyperv1.KubeAPIServerAvailable),
		Status: metav1.ConditionTrue,
	}
	gcpSpec := &hyperv1.GCPPlatformSpec{Project: "test-project", Region: "us-central1"}

	testCases := []struct {
		name               string
		regionErr          error
		expectedWIFStatus  metav1.ConditionStatus
		expectedCredStatus metav1.ConditionStatus
		expectError        bool
	}{
		{
			name:               "When WIF token exchange fails, it should set WIF to False and Credentials to Unknown",
			regionErr:          &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
			expectedWIFStatus:  metav1.ConditionFalse,
			expectedCredStatus: metav1.ConditionUnknown,
			expectError:        true,
		},
		{
			name:               "When Compute API returns 401, it should set WIF to True and Credentials to False",
			regionErr:          &googleapi.Error{Code: 401, Message: "Unauthorized"},
			expectedWIFStatus:  metav1.ConditionTrue,
			expectedCredStatus: metav1.ConditionFalse,
			expectError:        true,
		},
		{
			name:               "When Compute API returns transient error, it should set both conditions to Unknown",
			regionErr:          &googleapi.Error{Code: 500, Message: "Internal Server Error"},
			expectedWIFStatus:  metav1.ConditionUnknown,
			expectedCredStatus: metav1.ConditionUnknown,
			expectError:        true,
		},
		{
			name:               "When Compute API succeeds, it should set both conditions to True",
			regionErr:          nil,
			expectedWIFStatus:  metav1.ConditionTrue,
			expectedCredStatus: metav1.ConditionTrue,
			expectError:        false,
		},
		{
			name:               "When compute client is unavailable, it should set both conditions to Unknown without returning an error",
			regionErr:          errComputeClientUnavailable,
			expectedWIFStatus:  metav1.ConditionUnknown,
			expectedCredStatus: metav1.ConditionUnknown,
			expectError:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			injectedErr := tc.regionErr
			old := gcpRegionChecker
			gcpRegionChecker = func(_ context.Context, _, _ string) error { return injectedErr }
			defer func() { gcpRegionChecker = old }()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: gcpSpec},
				},
				Status: hyperv1.HostedControlPlaneStatus{Conditions: []metav1.Condition{}},
			}
			meta.SetStatusCondition(&hcp.Status.Conditions, *kasTrue)

			err := gcpHealthCheckIdentityProvider(t.Context(), hcp)
			if tc.expectError && err == nil {
				t.Fatal("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("expected no error but got: %v", err)
			}

			wifCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidGCPWorkloadIdentity))
			if wifCond == nil {
				t.Fatal("ValidGCPWorkloadIdentity condition was not set")
			}
			if wifCond.Status != tc.expectedWIFStatus {
				t.Errorf("ValidGCPWorkloadIdentity: expected status %v, got %v", tc.expectedWIFStatus, wifCond.Status)
			}

			credCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidGCPCredentials))
			if credCond == nil {
				t.Fatal("ValidGCPCredentials condition was not set")
			}
			if credCond.Status != tc.expectedCredStatus {
				t.Errorf("ValidGCPCredentials: expected status %v, got %v", tc.expectedCredStatus, credCond.Status)
			}
		})
	}
}

func TestIsWIFTokenError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "When OAuth2 returns HTTP 401, it should classify the error as a WIF token error",
			err:  &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
			want: true,
		},
		{
			name: "When OAuth2 returns HTTP 400, it should classify the error as a WIF token error",
			err:  &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}},
			want: true,
		},
		{
			name: "When OAuth2 returns HTTP 403, it should classify the error as a WIF token error",
			err:  &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusForbidden}},
			want: true,
		},
		{
			name: "When OAuth2 returns HTTP 429, it should not classify the error as a WIF token error",
			err:  &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
			want: false,
		},
		{
			name: "When a googleapi 401 error occurs, it should not classify the error as a WIF token error",
			err:  &googleapi.Error{Code: 401, Message: "Unauthorized"},
			want: false,
		},
		{
			name: "When a generic error occurs, it should not classify the error as a WIF token error",
			err:  fmt.Errorf("network timeout"),
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWIFTokenError(tc.err); got != tc.want {
				t.Errorf("isWIFTokenError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsComputeAuthError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "When a 401 googleapi error occurs, it should classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 401, Message: "Unauthorized"},
			want: true,
		},
		{
			name: "When a wrapped googleapi 401 error occurs, it should classify the error as a compute auth error",
			err:  fmt.Errorf("compute call failed: %w", &googleapi.Error{Code: 401}),
			want: true,
		},
		{
			name: "When a 403 googleapi error occurs, it should not classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 403, Message: "Forbidden"},
			want: false,
		},
		{
			name: "When a 403 googleapi error with rateLimitExceeded occurs, it should not classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}}},
			want: false,
		},
		{
			name: "When a 403 googleapi error with quotaExceeded occurs, it should not classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}}},
			want: false,
		},
		{
			name: "When a 403 googleapi error with accessNotConfigured occurs, it should not classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}},
			want: false,
		},
		{
			name: "When a 500 googleapi error occurs, it should not classify the error as a compute auth error",
			err:  &googleapi.Error{Code: 500, Message: "Internal Server Error"},
			want: false,
		},
		{
			name: "When an OAuth2 error occurs, it should not classify the error as a compute auth error",
			err:  &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}},
			want: false,
		},
		{
			name: "When a generic error occurs, it should not classify the error as a compute auth error",
			err:  fmt.Errorf("network timeout"),
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isComputeAuthError(tc.err); got != tc.want {
				t.Errorf("isComputeAuthError() = %v, want %v", got, tc.want)
			}
		})
	}
}
