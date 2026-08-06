package conditions

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExpectedHCConditionsGCPPlatform(t *testing.T) {
	tests := []struct {
		name               string
		hostedCluster      *hyperv1.HostedCluster
		expectedConditions map[hyperv1.ConditionType]metav1.ConditionStatus
	}{
		{
			name: "When platform is GCP, it should include ValidGCPWorkloadIdentity as True",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.ValidGCPWorkloadIdentity: metav1.ConditionTrue,
			},
		},
		{
			name: "When platform is GCP, it should include ValidGCPCredentials as True",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.ValidGCPCredentials: metav1.ConditionTrue,
			},
		},
		{
			name: "When platform is GCP, it should include GCPEndpointAvailable as True",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.GCPEndpointAvailable: metav1.ConditionTrue,
			},
		},
		{
			name: "When platform is GCP, it should include GCPServiceAttachmentAvailable as True",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.GCPServiceAttachmentAvailable: metav1.ConditionTrue,
			},
		},
		{
			name: "When platform is GCP, it should include all four GCP-specific conditions as True",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.ValidGCPWorkloadIdentity:      metav1.ConditionTrue,
				hyperv1.ValidGCPCredentials:           metav1.ConditionTrue,
				hyperv1.GCPEndpointAvailable:          metav1.ConditionTrue,
				hyperv1.GCPServiceAttachmentAvailable: metav1.ConditionTrue,
			},
		},
		{
			name: "When platform is not GCP, it should not include GCP-specific conditions",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.ValidGCPWorkloadIdentity:      "",
				hyperv1.ValidGCPCredentials:           "",
				hyperv1.GCPEndpointAvailable:          "",
				hyperv1.GCPServiceAttachmentAvailable: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := ExpectedHCConditions(tt.hostedCluster)
			for condType, expectedStatus := range tt.expectedConditions {
				if expectedStatus == "" {
					g.Expect(result).NotTo(HaveKey(condType),
						"condition %q should not be present for non-GCP platform", condType)
				} else {
					g.Expect(result).To(HaveKeyWithValue(condType, expectedStatus),
						"condition %q should be %q", condType, expectedStatus)
				}
			}
		})
	}
}
