package healthcheck

import (
	"context"
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func awsHealthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// Check if KAS is available before attempting to validate AWS identity provider.
	// The AWS identity provider validation requires a token to be minted, which requires KAS to be up.
	kasAvailable := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
	if kasAvailable == nil || kasAvailable.Status != metav1.ConditionTrue {
		// KAS is not available, cannot validate AWS identity provider
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            "Cannot validate AWS identity provider while KubeAPIServer is not available",
			Reason:             hyperv1.StatusUnknownReason,
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, condition)
		return nil
	}

	ec2Client, _, _ := hostedcontrolplane.GetEC2Client(ctx)
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

	// We try to interact with cloud provider to see validate is operational.
	if _, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{}); err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// When apiErr.ErrorCode() is WebIdentityErr it's likely to be an external issue, e.g. the idp resource was deleted.
			// We don't set apiErr.ErrorMessage() in the condition as it might contain aws requests IDs that would make the condition be updated in loop.
			if apiErr.ErrorCode() == "WebIdentityErr" {
				condition := metav1.Condition{
					Type:               string(hyperv1.ValidAWSIdentityProvider),
					ObservedGeneration: hcp.Generation,
					Status:             metav1.ConditionFalse,
					Message:            apiErr.ErrorCode(),
					Reason:             hyperv1.InvalidIdentityProvider,
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, condition)
				return fmt.Errorf("error health checking AWS identity provider: %s %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
			}

			condition := metav1.Condition{
				Type:               string(hyperv1.ValidAWSIdentityProvider),
				ObservedGeneration: hcp.Generation,
				Status:             metav1.ConditionUnknown,
				Message:            apiErr.ErrorCode(),
				Reason:             hyperv1.AWSErrorReason,
			}
			meta.SetStatusCondition(&hcp.Status.Conditions, condition)
			return fmt.Errorf("error health checking AWS identity provider: %s %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
		}

		condition := metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			ObservedGeneration: hcp.Generation,
			Status:             metav1.ConditionUnknown,
			Message:            err.Error(),
			Reason:             hyperv1.StatusUnknownReason,
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
