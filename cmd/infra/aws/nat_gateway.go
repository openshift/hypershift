package aws

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/awsapi"

	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"

	"github.com/go-logr/logr"
)

type natGatewayAttemptObserver struct {
	observed bool
	rejected bool
}

func (observer *natGatewayAttemptObserver) apiOption(options *ec2.Options) {
	options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error {
		err := stack.Finalize.Insert(middleware.FinalizeMiddlewareFunc("ObserveNATGatewayAttempts", func(ctx context.Context, input middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
			output, metadata, err := next.HandleFinalize(ctx, input)
			attempts, ok := awsretry.GetAttemptResults(metadata)
			observer.record(attempts, ok, err)
			return output, metadata, err
		}), "Retry", middleware.Before)
		if err != nil {
			observer.record(awsretry.AttemptResults{}, false, nil)
		}
		return nil
	})
}

func (observer *natGatewayAttemptObserver) record(attempts awsretry.AttemptResults, present bool, err error) {
	rejected := !observer.observed && present && len(attempts.Results) > 0 && isNATGatewayRejection(err)
	for _, attempt := range attempts.Results {
		rejected = rejected && isNATGatewayRejection(attempt.Err)
	}
	observer.observed = true
	observer.rejected = rejected
}

func isNATGatewayRejection(err error) bool {
	for err != nil {
		if canceled, ok := err.(interface{ CanceledError() bool }); ok && canceled.CanceledError() {
			return false
		}
		if connection, ok := err.(interface{ ConnectionError() bool }); ok && connection.ConnectionError() {
			return false
		}
		if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
			return false
		}
		if _, joined := err.(interface{ Unwrap() []error }); joined {
			return false
		}
		if inner := errors.Unwrap(err); inner != nil {
			err = inner
			continue
		}
		var apiErr smithy.APIError
		return errors.As(err, &apiErr) && (strings.EqualFold(apiErr.ErrorCode(), "NatGatewayLimitExceeded") ||
			strings.EqualFold(apiErr.ErrorCode(), invalidSubnet) ||
			strings.EqualFold(apiErr.ErrorCode(), invalidElasticIPNotFound))
	}
	return false
}

func releaseNATGatewayEIP(ctx context.Context, logger logr.Logger, client awsapi.EC2API, allocationID string, timeout time.Duration) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if _, err := client.ReleaseAddress(cleanupCtx, &ec2.ReleaseAddressInput{AllocationId: &allocationID}); err != nil {
		logger.Error(err, "Cannot release elastic IP after rejected NAT gateway creation", "allocationID", allocationID)
	}
}
