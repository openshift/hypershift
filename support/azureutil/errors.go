package azureutil

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Azure error codes
const (
	// AuthorizationFailedErrorCode is returned when the client lacks permission for an operation.
	AuthorizationFailedErrorCode = "AuthorizationFailed"
)

// IsNotFoundError returns true if the error represents an HTTP 404 Not Found response from Azure.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

// IsConflictError returns true if the error represents an HTTP 409 Conflict response from Azure.
func IsConflictError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict
}

// IsAuthorizationError returns true if the error represents an authorization failure from Azure.
// This includes HTTP 403 Forbidden responses and errors with the "AuthorizationFailed" error code.
func IsAuthorizationError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusForbidden || strings.EqualFold(respErr.ErrorCode, AuthorizationFailedErrorCode)
}

// ResponseErrorCode extracts the Azure error code from the error.
// Returns an empty string if the error is nil or not an Azure ResponseError.
func ResponseErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode
	}
	return ""
}
