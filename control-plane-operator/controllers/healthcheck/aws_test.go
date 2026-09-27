package healthcheck

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.uber.org/mock/gomock"
)

// TestAWSHealthCheckIdentityProviderEC2ClientNil tests that when the EC2 client
// is not available, awsHealthCheckIdentityProvider sets ValidAWSIdentityProvider
// to Unknown.
func TestAWSHealthCheckIdentityProviderEC2ClientNil(t *testing.T) {
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

	err := awsHealthCheckIdentityProvider(t.Context(), hcp, nil)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if condition == nil {
		t.Fatal("ValidAWSIdentityProvider condition was not set")
	}

	if condition.Status != metav1.ConditionUnknown {
		t.Errorf("Expected status %v, got %v", metav1.ConditionUnknown, condition.Status)
	}
	if condition.Reason != hyperv1.StatusUnknownReason {
		t.Errorf("Expected reason %v, got %v", hyperv1.StatusUnknownReason, condition.Reason)
	}
	expectedMessage := "AWS EC2 client is not available"
	if condition.Message != expectedMessage {
		t.Errorf("Expected message %q, got %q", expectedMessage, condition.Message)
	}
	if condition.ObservedGeneration != hcp.Generation {
		t.Errorf("Expected ObservedGeneration %v, got %v", hcp.Generation, condition.ObservedGeneration)
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

	// With no EC2 client available, awsHealthCheckIdentityProvider sets
	// ValidAWSIdentityProvider to Unknown and returns nil, so update()
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

	// No EC2 client is available, so the check sets Unknown status
	if condition.Status != metav1.ConditionUnknown {
		t.Errorf("Expected condition status %v, got %v", metav1.ConditionUnknown, condition.Status)
	}

}

func TestClassifyIdentityProviderError(t *testing.T) {
	testCases := []struct {
		name            string
		err             error
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name: "When error wraps STS AccessDenied, it should return False with InvalidIdentityProvider",
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials, %w",
					&smithy.GenericAPIError{Code: "AccessDenied", Message: "Not authorized"})),
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "AWS identity provider validation failed: AccessDenied: Not authorized",
		},
		{
			name: "When error wraps STS IDPRejectedClaim, it should return False with InvalidIdentityProvider",
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials, %w",
					&smithy.GenericAPIError{Code: "IDPRejectedClaim", Message: "claim rejected"})),
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "AWS identity provider validation failed: IDPRejectedClaim: claim rejected",
		},
		{
			name: "When error wraps STS ExpiredTokenException, it should return False with InvalidIdentityProvider",
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials, %w",
					&smithy.GenericAPIError{Code: "ExpiredTokenException", Message: "token expired"})),
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "AWS identity provider validation failed: ExpiredTokenException: token expired",
		},
		{
			name: "When error wraps InvalidIdentityToken, it should return Unknown with AWSErrorReason",
			// InvalidIdentityToken can be transient (STS can't fetch OIDC discovery
			// keys due to network/firewall/latency), so it is not in stsAuthErrorCodes.
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials, %w",
					&smithy.GenericAPIError{Code: "InvalidIdentityToken", Message: "token not valid"})),
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.AWSErrorReason,
			expectedMessage: "Cannot validate AWS identity provider: InvalidIdentityToken: token not valid",
		},
		{
			name: "When error wraps a non-STS API error like ThrottlingException, it should return Unknown with AWSErrorReason",
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				&smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"}),
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.AWSErrorReason,
			expectedMessage: "Cannot validate AWS identity provider: ThrottlingException: Rate exceeded",
		},
		{
			name: "When error wraps IDPCommunicationError, it should return Unknown with AWSErrorReason",
			// IDPCommunicationError means STS could not reach the identity provider.
			// Per AWS docs this is often transient, so it is not in stsAuthErrorCodes.
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials, %w",
					&smithy.GenericAPIError{Code: "IDPCommunicationError", Message: "could not communicate with IdP"})),
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.AWSErrorReason,
			expectedMessage: "Cannot validate AWS identity provider: IDPCommunicationError: could not communicate with IdP",
		},
		{
			name: "When error wraps a plain error with no API error, it should return Unknown with StatusUnknownReason",
			err: fmt.Errorf("operation error EC2: DescribeVpcEndpoints, %w",
				fmt.Errorf("failed to retrieve credentials: %w", errors.New("some unknown error"))),
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider: operation error EC2: DescribeVpcEndpoints, failed to retrieve credentials: some unknown error",
		},
		{
			name:            "When a non-API error contains RequestID and HostID, they should be scrubbed from the message",
			err:             fmt.Errorf("https response error StatusCode: 500, RequestID: abc-123-def, HostID: xyz-456-ghi, connection reset"),
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "Cannot validate AWS identity provider: https response error StatusCode: 500, connection reset",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, message := classifyIdentityProviderError(tc.err)
			if status != tc.expectedStatus {
				t.Errorf("Expected status %v, got %v", tc.expectedStatus, status)
			}
			if reason != tc.expectedReason {
				t.Errorf("Expected reason %q, got %q", tc.expectedReason, reason)
			}
			if message != tc.expectedMessage {
				t.Errorf("Expected message %q, got %q", tc.expectedMessage, message)
			}
		})
	}
}

// TestClassifyIdentityProviderErrorMessageStability verifies that errors
// differing only in RequestID produce identical condition messages. This is
// the core property that prevents the controller from churning the condition
// on every reconcile loop.
func TestClassifyIdentityProviderErrorMessageStability(t *testing.T) {
	// Simulate the real SDK error chain: OperationError → ResponseError → GenericAPIError.
	// ResponseError.Error() embeds the RequestID, but errors.As finds the inner
	// GenericAPIError whose ErrorCode()/ErrorMessage() are stable.
	makeError := func(requestID string) error {
		inner := &smithy.GenericAPIError{Code: "AccessDenied", Message: "Not authorized to perform sts:AssumeRoleWithWebIdentity"}
		return fmt.Errorf("operation error EC2: DescribeVpcEndpoints, https response error StatusCode: 403, RequestID: %s, %w", requestID, inner)
	}

	_, _, msg1 := classifyIdentityProviderError(makeError("aaaaaaaa-1111-1111-1111-aaaaaaaaaaaa"))
	_, _, msg2 := classifyIdentityProviderError(makeError("bbbbbbbb-2222-2222-2222-bbbbbbbbbbbb"))

	if msg1 != msg2 {
		t.Errorf("When two errors differ only in RequestID, they should produce identical messages, got:\n  %q\n  %q", msg1, msg2)
	}

	// Also verify the non-API-error fallback path scrubs RequestID and HostID
	// via the regex, preventing condition churn.
	makeNonAPIError := func(requestID, hostID string) error {
		return fmt.Errorf("https response error StatusCode: 500, RequestID: %s, HostID: %s, connection reset", requestID, hostID)
	}

	_, _, msg3 := classifyIdentityProviderError(makeNonAPIError("aaaa-1111", "host-aaaa"))
	_, _, msg4 := classifyIdentityProviderError(makeNonAPIError("bbbb-2222", "host-bbbb"))

	if msg3 != msg4 {
		t.Errorf("When two non-API errors differ only in RequestID/HostID, they should produce identical messages, got:\n  %q\n  %q", msg3, msg4)
	}
}

func TestAWSHealthCheckIdentityProviderWithMockEC2(t *testing.T) {
	testCases := []struct {
		name             string
		describeErr      error
		expectedStatus   metav1.ConditionStatus
		expectedReason   string
		expectedErrNil   bool
		messageSubstring string
	}{
		{
			name:             "When DescribeVpcEndpoints succeeds, it should set condition True with AsExpected",
			describeErr:      nil,
			expectedStatus:   metav1.ConditionTrue,
			expectedReason:   hyperv1.AsExpectedReason,
			expectedErrNil:   true,
			messageSubstring: hyperv1.AllIsWellMessage,
		},
		{
			name:             "When DescribeVpcEndpoints returns STS AccessDenied, it should set condition False with InvalidIdentityProvider",
			describeErr:      &smithy.GenericAPIError{Code: "AccessDenied", Message: "Not authorized"},
			expectedStatus:   metav1.ConditionFalse,
			expectedReason:   hyperv1.InvalidIdentityProvider,
			expectedErrNil:   false,
			messageSubstring: "AccessDenied",
		},
		{
			name:             "When DescribeVpcEndpoints returns STS ExpiredTokenException, it should set condition False with InvalidIdentityProvider",
			describeErr:      &smithy.GenericAPIError{Code: "ExpiredTokenException", Message: "token expired"},
			expectedStatus:   metav1.ConditionFalse,
			expectedReason:   hyperv1.InvalidIdentityProvider,
			expectedErrNil:   false,
			messageSubstring: "ExpiredTokenException",
		},
		{
			name:             "When DescribeVpcEndpoints returns InvalidIdentityToken, it should set condition Unknown with AWSErrorReason",
			describeErr:      &smithy.GenericAPIError{Code: "InvalidIdentityToken", Message: "token not valid"},
			expectedStatus:   metav1.ConditionUnknown,
			expectedReason:   hyperv1.AWSErrorReason,
			expectedErrNil:   false,
			messageSubstring: "InvalidIdentityToken",
		},
		{
			name:             "When DescribeVpcEndpoints returns ThrottlingException, it should set condition Unknown with AWSErrorReason",
			describeErr:      &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"},
			expectedStatus:   metav1.ConditionUnknown,
			expectedReason:   hyperv1.AWSErrorReason,
			expectedErrNil:   false,
			messageSubstring: "ThrottlingException",
		},
		{
			name:             "When DescribeVpcEndpoints returns a non-API error, it should set condition Unknown with StatusUnknownReason",
			describeErr:      errors.New("network timeout"),
			expectedStatus:   metav1.ConditionUnknown,
			expectedReason:   hyperv1.StatusUnknownReason,
			expectedErrNil:   false,
			messageSubstring: "network timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockEC2 := awsapi.NewMockEC2API(mockCtrl)

			var describeOutput *ec2.DescribeVpcEndpointsOutput
			if tc.describeErr == nil {
				describeOutput = &ec2.DescribeVpcEndpointsOutput{}
			}
			mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(describeOutput, tc.describeErr)

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

			err := awsHealthCheckIdentityProvider(t.Context(), hcp, mockEC2)

			if tc.expectedErrNil && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
			if !tc.expectedErrNil && err == nil {
				t.Error("Expected an error, got nil")
			}

			condition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
			if condition == nil {
				t.Fatal("ValidAWSIdentityProvider condition was not set")
			}
			if condition.Status != tc.expectedStatus {
				t.Errorf("Expected status %v, got %v", tc.expectedStatus, condition.Status)
			}
			if condition.Reason != tc.expectedReason {
				t.Errorf("Expected reason %q, got %q", tc.expectedReason, condition.Reason)
			}
			if tc.messageSubstring != "" && !strings.Contains(condition.Message, tc.messageSubstring) {
				t.Errorf("Expected message to contain %q, got %q", tc.messageSubstring, condition.Message)
			}
			if condition.ObservedGeneration != hcp.Generation {
				t.Errorf("Expected ObservedGeneration %v, got %v", hcp.Generation, condition.ObservedGeneration)
			}
		})
	}
}
