package healthcheck

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAzureHealthCheckIdentityProviderConditionLogic(t *testing.T) {
	testCases := []struct {
		name            string
		azureConfig     *hyperv1.AzurePlatformSpec
		kasCondition    *metav1.Condition
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name:            "Azure config nil",
			azureConfig:     nil,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Azure platform configuration is missing",
		},
		{
			name:            "KAS not available - condition missing",
			azureConfig:     &hyperv1.AzurePlatformSpec{},
			kasCondition:    nil,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate Azure credentials while KubeAPIServer is not available",
		},
		{
			name:        "KAS not available - condition False",
			azureConfig: &hyperv1.AzurePlatformSpec{},
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate Azure credentials while KubeAPIServer is not available",
		},
		{
			name:        "KAS available but credentials nil",
			azureConfig: &hyperv1.AzurePlatformSpec{},
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Azure credentials are not available for validation",
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
						Azure: tc.azureConfig,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{},
				},
			}

			if tc.kasCondition != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tc.kasCondition)
			}

			// Pass nil credentials — all test cases should return before the Azure call
			err := azureHealthCheckCredentials(t.Context(), hcp, nil)
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAzureIdentityProvider))
			if condition == nil {
				t.Fatal("ValidAzureIdentityProvider condition was not set")
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
