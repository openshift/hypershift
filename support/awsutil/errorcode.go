package awsutil

import (
	"errors"

	"github.com/aws/smithy-go"
)

const (
	AccessDenied          = "AccessDenied"
	AuthFailure           = "AuthFailure"
	DependencyViolation   = "DependencyViolation"
	ExpiredTokenException = "ExpiredTokenException"
	IDPRejectedClaim      = "IDPRejectedClaim"
	InvalidIdentityToken  = "InvalidIdentityToken"
	UnauthorizedOperation = "UnauthorizedOperation"
	// AccessDenied and NotAuthorizedException are how Route53 (and other
	// non-EC2 services) report permission errors, versus EC2's AuthFailure /
	// UnauthorizedOperation.
	AccessDenied           = "AccessDenied"
	NotAuthorizedException = "NotAuthorizedException"
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
	return code == AuthFailure || code == UnauthorizedOperation ||
		code == AccessDenied || code == NotAuthorizedException
}
