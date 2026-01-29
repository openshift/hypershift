// Package testutil provides test utilities for the HyperShift project.
//
// This file contains Azure SDK test utilities that follow Azure SDK for Go testing patterns.
// The Azure SDK recommends mocking at the HTTP transport level using custom
// policy.Transporter implementations. This allows testing without modifying
// production code structure.
//
// References:
//   - Azure SDK Design Guidelines for Go: https://azure.github.io/azure-sdk/golang_introduction.html
//   - Azure SDK internal mock package: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/internal/mock
//   - Azure SDK fake server examples: https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/samples/fakes
//   - policy.Transporter interface: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azcore/policy#Transporter
//   - azcore.TokenCredential interface: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azcore#TokenCredential
package testutil

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Compile-time interface checks
var (
	_ azcore.TokenCredential = (*FakeAzureCredential)(nil)
	_ policy.Transporter     = (*MockAzureTransport)(nil)
	_ policy.Transporter     = (*MockAzureResourceGroupTransport)(nil)
)

// Azure error response bodies for testing
const (
	resourceGroupNotFoundBody = `{"error":{"code":"ResourceGroupNotFound","message":"Resource group could not be found."}}`
	authorizationFailedBody   = `{"error":{"code":"AuthorizationFailed","message":"The client does not have authorization to perform action."}}`
)

// FakeAzureCredential implements azcore.TokenCredential for testing Azure SDK clients.
// It returns a fake token without making any network requests.
//
// This follows the Azure SDK pattern for mocking credentials in unit tests.
// See: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azcore#TokenCredential
type FakeAzureCredential struct{}

// GetToken returns a fake access token for testing.
func (f *FakeAzureCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "fake-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// MockAzureTransport implements policy.Transporter for testing Azure SDK clients.
// It returns preconfigured HTTP responses based on the status code.
//
// The Azure SDK uses policy.Transporter as the HTTP transport abstraction,
// allowing injection of mock transports for testing. This is the recommended
// approach per Azure SDK design guidelines.
// See: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azcore/policy#Transporter
type MockAzureTransport struct {
	StatusCode   int
	ResponseBody string
}

// Do returns a mock HTTP response with the configured status code and body.
func (m *MockAzureTransport) Do(req *http.Request) (*http.Response, error) {
	return newJSONResponse(req, m.StatusCode, m.ResponseBody), nil
}

// newJSONResponse creates an HTTP response with JSON content type.
func newJSONResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}
}

// NewAzureNotFoundTransport creates a MockAzureTransport that returns 404 ResourceGroupNotFound errors.
func NewAzureNotFoundTransport() *MockAzureTransport {
	return &MockAzureTransport{
		StatusCode:   http.StatusNotFound,
		ResponseBody: resourceGroupNotFoundBody,
	}
}

// NewAzureForbiddenTransport creates a MockAzureTransport that returns 403 AuthorizationFailed errors.
func NewAzureForbiddenTransport() *MockAzureTransport {
	return &MockAzureTransport{
		StatusCode:   http.StatusForbidden,
		ResponseBody: authorizationFailedBody,
	}
}

// NewAzureSuccessTransport creates a MockAzureTransport that returns 200 OK with an empty response.
func NewAzureSuccessTransport() *MockAzureTransport {
	return &MockAzureTransport{
		StatusCode:   http.StatusOK,
		ResponseBody: `{}`,
	}
}

// MockAzureResourceGroupTransport implements policy.Transporter with proper handling
// for Azure resource group operations including LROs (Long Running Operations).
type MockAzureResourceGroupTransport struct {
	// DeleteResponse is the status code returned for DELETE operations.
	// Use http.StatusOK for immediate success, http.StatusNotFound for not found.
	DeleteResponse int
}

// Do handles Azure resource group API requests with proper response formats.
func (m *MockAzureResourceGroupTransport) Do(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodHead:
		// CheckExistence uses HEAD - return 204 for exists, 404 for not found
		if m.DeleteResponse == http.StatusNotFound {
			return newJSONResponse(req, http.StatusNotFound, resourceGroupNotFoundBody), nil
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
			Header:     http.Header{},
			Request:    req,
		}, nil

	case http.MethodDelete:
		// BeginDelete - return configured response
		if m.DeleteResponse == http.StatusNotFound {
			return newJSONResponse(req, http.StatusNotFound, resourceGroupNotFoundBody), nil
		}
		return newJSONResponse(req, http.StatusOK, "{}"), nil

	default:
		return newJSONResponse(req, http.StatusOK, "{}"), nil
	}
}

// NewAzureResourceGroupSuccessTransport creates a transport that simulates successful
// resource group deletion (resource group exists and is deleted).
func NewAzureResourceGroupSuccessTransport() *MockAzureResourceGroupTransport {
	return &MockAzureResourceGroupTransport{
		DeleteResponse: http.StatusOK,
	}
}

// NewAzureResourceGroupNotFoundTransport creates a transport that simulates
// resource group not found (404) responses.
func NewAzureResourceGroupNotFoundTransport() *MockAzureResourceGroupTransport {
	return &MockAzureResourceGroupTransport{
		DeleteResponse: http.StatusNotFound,
	}
}
