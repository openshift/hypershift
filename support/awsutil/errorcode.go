package awsutil

import (
	"errors"

	"github.com/aws/smithy-go"
)

const (
	AuthFailure           = "AuthFailure"
	UnauthorizedOperation = "UnauthorizedOperation"
)

func AWSErrorCode(err error) string {
	var smithyErr smithy.APIError
	if errors.As(err, &smithyErr) {
		return smithyErr.ErrorCode()
	}
	return "Unknown"
}

// IsPermissionsError returns true if on aws permission errors.
func IsPermissionsError(err error) bool {
	code := AWSErrorCode(err)
	return code == AuthFailure || code == UnauthorizedOperation
}
