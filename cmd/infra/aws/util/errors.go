package util

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws/awserr"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func IsErrorRetryable(err error) bool {
	if aggregate, isAggregate := err.(utilerrors.Aggregate); isAggregate {
		if len(aggregate.Errors()) == 1 {
			err = aggregate.Errors()[0]
		} else {
			// We aggregate all errors, utilerrors.Aggregate does for safety reasons not support
			// errors.As (As it can't know what to do when there are multiple matches), so we
			// iterate and bail out if there are only credential load errors
			hasOnlyCredentialLoadErrors := true
			for _, err := range aggregate.Errors() {
				if !isCredentialLoadError(err) {
					hasOnlyCredentialLoadErrors = false
					break
				}
			}
			if hasOnlyCredentialLoadErrors {
				return false
			}

		}
	}

	if isCredentialLoadError(err) {
		return false
	}
	return true
}

func isCredentialLoadError(err error) bool {
	if awsErr := awserr.Error(nil); errors.As(err, &awsErr) && awsErr.Code() == "SharedCredsLoad" {
		return true
	}

	return false
}
