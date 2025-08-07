package awsutil

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws/awserr"
)

const (
	AuthFailure           = "AuthFailure"
	UnauthorizedOperation = "UnauthorizedOperation"
)

func AWSErrorCode(err error) string {
	var awsErr awserr.Error
	if errors.As(err, &awsErr) {
		return awsErr.Code()
	}
	return "Unknown"
}

// IsPermissionsError returns true if on aws permission errors.
func IsPermissionsError(err error) bool {
	code := AWSErrorCode(err)
	return code == AuthFailure || code == UnauthorizedOperation
}
