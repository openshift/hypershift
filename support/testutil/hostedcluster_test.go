package testutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewHostedClusterWithCredentialConditions(t *testing.T) {
	hc := NewHostedClusterWithCredentialConditions(metav1.ConditionFalse, metav1.ConditionTrue)

	if hc.Name != "test" || hc.Namespace != "clusters" {
		t.Errorf("expected HostedCluster to have name %q and namespace %q, got %q/%q", "test", "clusters", hc.Namespace, hc.Name)
	}

	tests := []struct {
		name          string
		conditionType string
		expected      metav1.ConditionStatus
	}{
		{
			name:          "When OIDC status is false, it should set the OIDC condition to false",
			conditionType: string(hyperv1.ValidOIDCConfiguration),
			expected:      metav1.ConditionFalse,
		},
		{
			name:          "When AWS identity provider status is true, it should set the AWS identity provider condition to true",
			conditionType: string(hyperv1.ValidAWSIdentityProvider),
			expected:      metav1.ConditionTrue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			condition := meta.FindStatusCondition(hc.Status.Conditions, tc.conditionType)
			if condition == nil {
				t.Fatalf("expected condition %q to be present", tc.conditionType)
			}
			if condition.Status != tc.expected {
				t.Errorf("expected condition %q to have status %q, got %q", tc.conditionType, tc.expected, condition.Status)
			}
			if condition.Reason != "test" {
				t.Errorf("expected condition %q to have reason %q, got %q", tc.conditionType, "test", condition.Reason)
			}
		})
	}
}
