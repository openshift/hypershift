package healthcheck

import (
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.uber.org/mock/gomock"
)

// TestAWSHealthCheckIdentityProviderConditionLogic tests the condition setting logic
// for the ValidAWSIdentityProvider condition across different code paths.
func TestAWSHealthCheckIdentityProviderConditionLogic(t *testing.T) {
	testCases := []struct {
		name            string
		kasCondition    *metav1.Condition
		setupEC2Mock    func(*gomock.Controller) awsapi.EC2API
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
		expectError     bool
	}{
		{
			name:            "When KAS condition is missing, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition:    nil,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "When KAS condition is False, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "When KAS condition is Unknown, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionUnknown,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider while KubeAPIServer is not available",
		},
		{
			name: "When KAS is available but EC2 client is nil, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "AWS EC2 client is not available",
		},
		{
			name: "When DescribeVpcEndpoints returns WebIdentityErr, it should set ValidAWSIdentityProvider to False",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			setupEC2Mock: func(ctrl *gomock.Controller) awsapi.EC2API {
				m := awsapi.NewMockEC2API(ctrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).
					Return(nil, &smithy.GenericAPIError{
						Code:    "WebIdentityErr",
						Message: "some AWS request details",
					})
				return m
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "WebIdentityErr",
			expectError:     true,
		},
		{
			name: "When DescribeVpcEndpoints returns a non-WebIdentityErr API error, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			setupEC2Mock: func(ctrl *gomock.Controller) awsapi.EC2API {
				m := awsapi.NewMockEC2API(ctrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).
					Return(nil, &smithy.GenericAPIError{
						Code:    "UnauthorizedAccess",
						Message: "access denied",
					})
				return m
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.AWSErrorReason,
			expectedMessage: "UnauthorizedAccess",
			expectError:     true,
		},
		{
			name: "When DescribeVpcEndpoints returns a non-API error, it should set ValidAWSIdentityProvider to Unknown",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			setupEC2Mock: func(ctrl *gomock.Controller) awsapi.EC2API {
				m := awsapi.NewMockEC2API(ctrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("network timeout"))
				return m
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "network timeout",
			expectError:     true,
		},
		{
			name: "When DescribeVpcEndpoints succeeds, it should set ValidAWSIdentityProvider to True",
			kasCondition: &metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionTrue,
			},
			setupEC2Mock: func(ctrl *gomock.Controller) awsapi.EC2API {
				m := awsapi.NewMockEC2API(ctrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).
					Return(&ec2.DescribeVpcEndpointsOutput{}, nil)
				return m
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  hyperv1.AsExpectedReason,
			expectedMessage: hyperv1.AllIsWellMessage,
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
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{},
				},
			}

			if tc.kasCondition != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tc.kasCondition)
			}

			kasAvailable := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
			if kasAvailable != nil && kasAvailable.Status == metav1.ConditionTrue {
				var ec2Client awsapi.EC2API
				if tc.setupEC2Mock != nil {
					mockCtrl := gomock.NewController(t)
					ec2Client = tc.setupEC2Mock(mockCtrl)
				}
				err := validateAWSIdentityProvider(t.Context(), hcp, ec2Client)
				if tc.expectError && err == nil {
					t.Error("expected error but got nil")
				}
				if !tc.expectError && err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			} else {
				err := awsHealthCheckIdentityProvider(t.Context(), hcp)
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}

			condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
			if condition == nil {
				t.Fatal("ValidAWSIdentityProvider condition was not set")
			}

			if condition.Status != tc.expectedStatus {
				t.Errorf("expected status %v, got %v", tc.expectedStatus, condition.Status)
			}

			if condition.Reason != tc.expectedReason {
				t.Errorf("expected reason %v, got %v", tc.expectedReason, condition.Reason)
			}

			if tc.expectedMessage != "" && condition.Message != tc.expectedMessage {
				t.Errorf("expected message %q, got %q", tc.expectedMessage, condition.Message)
			}

			if condition.ObservedGeneration != hcp.Generation {
				t.Errorf("expected ObservedGeneration %v, got %v", hcp.Generation, condition.ObservedGeneration)
			}
		})
	}
}
