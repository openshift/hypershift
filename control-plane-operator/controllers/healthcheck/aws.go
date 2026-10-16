package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func awsHealthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane, ec2Client awsapi.EC2API) error {
	if ec2Client == nil {
		// EC2 client is not available (token minting may have failed)
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            "AWS EC2 client is not available",
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return nil
	}

	// We try to interact with cloud provider to validate it is operational.
	if _, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{}); err != nil {
		status, reason, message := classifyIdentityProviderError(err)
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			ObservedGeneration: hcp.Generation,
			Status:             status,
			Reason:             reason,
			Message:            message,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return fmt.Errorf("error health checking AWS identity provider: %w", err)
	}

	condition := metav1.Condition{
		Type:               string(hyperv1.ValidAWSIdentityProvider),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionTrue,
		Message:            hyperv1.AllIsWellMessage,
		Reason:             hyperv1.AsExpectedReason,
	}
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)

	return nil
}

// stsAuthErrorCodes are STS error codes that indicate the identity provider
// is genuinely invalid (e.g. deleted OIDC provider, wrong role trust policy).
//
// Intentionally excluded (classified as Unknown — fail safe):
//   - InvalidIdentityToken: STS returns it both for genuinely invalid tokens
//     AND for transient infrastructure issues (e.g. STS cannot fetch OIDC
//     discovery keys due to network/firewall/latency).
//   - IDPCommunicationError: STS could not reach the identity provider; often
//     a transient network condition per AWS documentation.
var stsAuthErrorCodes = sets.New[string](
	supportawsutil.AccessDenied,
	supportawsutil.ExpiredTokenException,
	supportawsutil.IDPRejectedClaim,
)

// requestIDPattern scrubs per-request IDs the SDK embeds via the ResponseError
// wrapper — only needed for non-API-error fall-through, in case a ResponseError
// was flattened into the string with %v.
var requestIDPattern = regexp.MustCompile(`,?\s*(?:RequestID|HostID): [^\s,]+`)

// classifyIdentityProviderError inspects an error from an AWS API call and
// returns the condition (status, reason, message) for ValidAWSIdentityProvider.
//
// After the migration to AWS SDK v2, credential errors from
// WebIdentityRoleProvider.Retrieve are wrapped with plain fmt.Errorf — no
// smithy.APIError with code "WebIdentityErr" is produced. This function uses
// errors.As to find the smithy.APIError in the chain and classify by code.
//
// The RequestID lives in the ResponseError wrapper, not in GenericAPIError's
// ErrorCode()/ErrorMessage(), so using the structured fields produces a stable
// condition message that won't cause reconciliation loops.
func classifyIdentityProviderError(err error) (metav1.ConditionStatus, string, string) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if stsAuthErrorCodes.Has(code) {
			return metav1.ConditionFalse, hyperv1.InvalidIdentityProvider,
				fmt.Sprintf("AWS identity provider validation failed: %s: %s", code, apiErr.ErrorMessage())
		}
		// Any other AWS API error (throttling, InvalidIdentityToken, ...) can't
		// prove the provider is invalid.
		return metav1.ConditionUnknown, hyperv1.AWSErrorReason,
			fmt.Sprintf("Cannot validate AWS identity provider: %s: %s", code, apiErr.ErrorMessage())
	}

	// Not an AWS API error: missing/unreadable token file, network timeout, KAS
	// blip, etc. Sanitize defensively to strip any request IDs.
	return metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
		fmt.Sprintf("Cannot validate AWS identity provider: %s", requestIDPattern.ReplaceAllString(err.Error(), ""))
}
