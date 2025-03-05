package healthcheck

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func awsHealthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	ec2Client, _ := hostedcontrolplane.GetEC2Client()
	if ec2Client == nil {
		return nil
	}

	// We try to interact with cloud provider to see validate is operational.
	if _, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{}); err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			// When awsErr.Code() is WebIdentityErr it's likely to be an external issue, e.g. the idp resource was deleted.
			// We don't set awsErr.Message() in the condition as it might contain aws requests IDs that would make the condition be updated in loop.
			if awsErr.Code() == "WebIdentityErr" {
				condition := metav1.Condition{
					Type:               string(hyperv1.ValidAWSIdentityProvider),
					ObservedGeneration: hcp.Generation,
					Status:             metav1.ConditionFalse,
					Message:            awsErr.Code(),
					Reason:             hyperv1.InvalidIdentityProvider,
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, condition)
				return fmt.Errorf("error health checking AWS identity provider: %s %s", awsErr.Code(), awsErr.Message())
			}

			condition := metav1.Condition{
				Type:               string(hyperv1.ValidAWSIdentityProvider),
				ObservedGeneration: hcp.Generation,
				Status:             metav1.ConditionUnknown,
				Message:            awsErr.Code(),
				Reason:             hyperv1.AWSErrorReason,
			}
			meta.SetStatusCondition(&hcp.Status.Conditions, condition)
			return fmt.Errorf("error health checking AWS identity provider: %s %s", awsErr.Code(), awsErr.Message())
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
