package conditions

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExpectedHCConditions(t *testing.T) {
	tests := []struct {
		name               string
		hostedCluster      *hyperv1.HostedCluster
		expectedConditions map[hyperv1.ConditionType]metav1.ConditionStatus
		unexpectedTypes    []hyperv1.ConditionType
	}{
		{
			name: "When platform is GCP, it should include GCP credential and PSC conditions",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
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
			name: "When platform is AWS with private endpoint, it should include AWS endpoint conditions",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
						},
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			expectedConditions: map[hyperv1.ConditionType]metav1.ConditionStatus{
				hyperv1.ValidOIDCConfiguration:         metav1.ConditionTrue,
				hyperv1.ValidAWSIdentityProvider:       metav1.ConditionTrue,
				hyperv1.AWSDefaultSecurityGroupCreated: metav1.ConditionTrue,
				hyperv1.AWSEndpointAvailable:           metav1.ConditionTrue,
				hyperv1.AWSEndpointServiceAvailable:    metav1.ConditionTrue,
				hyperv1.ValidAWSKMSConfig:              metav1.ConditionUnknown,
			},
			unexpectedTypes: []hyperv1.ConditionType{
				hyperv1.ValidGCPWorkloadIdentity,
				hyperv1.GCPEndpointAvailable,
			},
		},
		{
			name: "When platform is GCP, it should not include AWS-specific conditions",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			unexpectedTypes: []hyperv1.ConditionType{
				hyperv1.ValidOIDCConfiguration,
				hyperv1.ValidAWSIdentityProvider,
				hyperv1.AWSEndpointAvailable,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := ExpectedHCConditions(tt.hostedCluster)

			for condType, expectedStatus := range tt.expectedConditions {
				status, exists := result[condType]
				g.Expect(exists).To(BeTrue(), "condition %s should be present", condType)
				g.Expect(status).To(Equal(expectedStatus), "condition %s should have status %s", condType, expectedStatus)
			}

			for _, condType := range tt.unexpectedTypes {
				_, exists := result[condType]
				g.Expect(exists).To(BeFalse(), "condition %s should not be present", condType)
			}
		})
	}
}
