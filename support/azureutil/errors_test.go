package azureutil

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is 404 ResponseError it should return true",
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expected: true,
		},
		{
			name:     "When error is 500 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
			expected: false,
		},
		{
			name:     "When error is 409 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusConflict},
			expected: false,
		},
		{
			name:     "When error is generic error it should return false",
			err:      errors.New("some generic error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("IsNotFoundError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is 409 ResponseError it should return true",
			err:      &azcore.ResponseError{StatusCode: http.StatusConflict},
			expected: true,
		},
		{
			name:     "When error is 404 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expected: false,
		},
		{
			name:     "When error is 500 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
			expected: false,
		},
		{
			name:     "When error is generic error it should return false",
			err:      errors.New("some generic error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsConflictError(tt.err)
			if result != tt.expected {
				t.Errorf("IsConflictError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsAuthorizationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is 403 ResponseError it should return true",
			err:      &azcore.ResponseError{StatusCode: http.StatusForbidden},
			expected: true,
		},
		{
			name:     "When error has AuthorizationFailed error code it should return true",
			err:      &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: AuthorizationFailedErrorCode},
			expected: true,
		},
		{
			name:     "When error has authorizationfailed error code with different case it should return true",
			err:      &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "authorizationfailed"},
			expected: true,
		},
		{
			name:     "When error is 404 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expected: false,
		},
		{
			name:     "When error is 500 ResponseError it should return false",
			err:      &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
			expected: false,
		},
		{
			name:     "When error is generic error it should return false",
			err:      errors.New("some generic error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAuthorizationError(tt.err)
			if result != tt.expected {
				t.Errorf("IsAuthorizationError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestResponseErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "When error is nil it should return empty string",
			err:      nil,
			expected: "",
		},
		{
			name:     "When error is ResponseError with code it should return the code",
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "ResourceNotFound"},
			expected: "ResourceNotFound",
		},
		{
			name:     "When error is ResponseError with AuthorizationFailed code it should return the code",
			err:      &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: AuthorizationFailedErrorCode},
			expected: AuthorizationFailedErrorCode,
		},
		{
			name:     "When error is ResponseError without code it should return empty string",
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expected: "",
		},
		{
			name:     "When error is generic error it should return empty string",
			err:      errors.New("some generic error"),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResponseErrorCode(tt.err)
			if result != tt.expected {
				t.Errorf("ResponseErrorCode() = %v, want %v", result, tt.expected)
			}
		})
	}
}
