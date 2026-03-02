package awsutil

import (
	"errors"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/smithy-go"
)

const (
	AuthFailure           = "AuthFailure"
	UnauthorizedOperation = "UnauthorizedOperation"
)

func AWSErrorCode(err error) string {
	// aws-sdk-go-v2 errors implement smithy.APIError
	var smithyErr smithy.APIError
	if errors.As(err, &smithyErr) {
		return smithyErr.ErrorCode()
	}
	// aws-sdk-go (v1) errors implement awserr.Error; keep until all services are migrated
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
