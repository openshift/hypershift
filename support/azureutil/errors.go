package azureutil

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const (
	// defaultRateLimitRetryAfter is the fallback requeue duration for 429 responses
	// when no Retry-After header is present or parseable.
	defaultRateLimitRetryAfter = 5 * time.Minute
)

// IsAzureNotFoundError checks if an error is an Azure 404 Not Found response.
func IsAzureNotFoundError(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

// ParseRetryAfterDuration extracts and parses the Retry-After header from an
// Azure ResponseError's raw HTTP response. Azure 429 responses typically include
// this header to indicate how long the client should wait before retrying.
//
// The Retry-After header supports two formats per RFC 7231 section 7.1.3:
//   - Seconds: a non-negative decimal integer (e.g., "120")
//   - HTTP-date: an absolute timestamp (e.g., "Thu, 01 Dec 2025 16:00:00 GMT")
//
// Returns the parsed duration and true if successful, or (0, false) if:
//   - The error is not an *azcore.ResponseError
//   - The RawResponse is nil
//   - The Retry-After header is absent or empty
//   - The header value cannot be parsed in either format
//   - The parsed HTTP-date is in the past (returns 0, false)
func ParseRetryAfterDuration(err error) (time.Duration, bool) {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return 0, false
	}

	if respErr.RawResponse == nil {
		return 0, false
	}

	retryAfter := respErr.RawResponse.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0, false
	}

	// Try parsing as seconds first (most common for Azure)
	if seconds, parseErr := strconv.ParseInt(retryAfter, 10, 64); parseErr == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, true
	}

	// Try parsing as HTTP-date (RFC 7231 section 7.1.1.1)
	if t, parseErr := http.ParseTime(retryAfter); parseErr == nil {
		d := time.Until(t)
		if d > 0 {
			return d, true
		}
		return 0, false
	}

	return 0, false
}

// ClassifyAzureError inspects an Azure API error and returns an appropriate
// requeue duration and human-readable message. This provides differentiated
// backoff instead of relying on controller-runtime's exponential backoff:
//   - 429 (Too Many Requests): uses Retry-After header if present, otherwise 5 minutes
//   - 403 (Forbidden): 10 minutes - permissions issues unlikely to self-resolve quickly
//   - 409 (Conflict): 30 seconds - transient conflict, retry soon
//   - Other Azure API errors: 2 minutes - general retry interval
//   - Non-Azure errors: 2 minutes - general retry interval
func ClassifyAzureError(err error) (requeueAfter time.Duration, message string) {
	if err == nil {
		return 0, ""
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case http.StatusTooManyRequests:
			retryAfter := defaultRateLimitRetryAfter
			if d, ok := ParseRetryAfterDuration(err); ok {
				retryAfter = d
			}
			return retryAfter, "Azure API rate limit exceeded, retrying"
		case http.StatusForbidden:
			return 10 * time.Minute, "Azure API permission denied, check service principal permissions"
		case http.StatusConflict:
			return 30 * time.Second, "Azure resource conflict, retrying"
		default:
			return 2 * time.Minute, fmt.Sprintf("Azure API error (HTTP %d): %s", respErr.StatusCode, respErr.ErrorCode)
		}
	}
	return 2 * time.Minute, fmt.Sprintf("Unexpected error: %s", err.Error())
}
