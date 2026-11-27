package azureutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	// defaultRateLimitRetryAfter is the fallback requeue duration for 429 responses
	// when no Retry-After header is present or parseable.
	defaultRateLimitRetryAfter = 5 * time.Minute

	// Azure ARM error codes used for diagnostic classification.
	ErrorCodeRequestDisallowedByPolicy = "RequestDisallowedByPolicy"
	ErrorCodeQuotaExceeded             = "QuotaExceeded"
	ErrorCodeOperationNotAllowed       = "OperationNotAllowed"
	ErrorCodeAllocationFailed          = "AllocationFailed"
	ErrorCodeConflictingResourceName   = "ConflictingResourceName"
	ErrorCodeRoleAssignmentExists      = "RoleAssignmentExists"
	ErrorCodeSkuNotAvailable           = "SkuNotAvailable"
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
// requeue duration and human-readable message. Classification order:
//
//  1. HTTP 429 first — always rate-limiting regardless of ErrorCode. This is
//     critical because OperationNotAllowed is used for both quota and throttling.
//  2. ErrorCode switch — specific enriched messages with appropriate requeue intervals.
//  3. StatusCode fallback — existing behavior for unrecognized ErrorCodes.
//
// Callers format as fmt.Sprintf("%s: %v", message, err), so the enriched message
// flows through automatically without caller changes.
func ClassifyAzureError(err error) (requeueAfter time.Duration, message string) {
	if err == nil {
		return 0, ""
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		// 1. HTTP 429 always means rate-limiting, regardless of ErrorCode.
		if respErr.StatusCode == http.StatusTooManyRequests {
			retryAfter := defaultRateLimitRetryAfter
			if d, ok := ParseRetryAfterDuration(err); ok {
				retryAfter = d
			}
			return retryAfter, "Azure API rate limit exceeded, retrying"
		}

		// 2. ErrorCode-based classification for known ARM error codes, preserving
		// existing status-code-based requeue intervals.
		if respErr.ErrorCode != "" {
			if message, ok := classifyByErrorCode(respErr.ErrorCode, err); ok {
				return requeueDurationForStatusCode(respErr.StatusCode), message
			}
		}

		// 3. StatusCode fallback for unrecognized ErrorCodes.
		switch respErr.StatusCode {
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

func requeueDurationForStatusCode(statusCode int) time.Duration {
	switch statusCode {
	case http.StatusForbidden:
		return 10 * time.Minute
	case http.StatusConflict:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}

// classifyByErrorCode returns (message, true) when the ErrorCode maps to a
// known diagnostic classification, or ("", false) when unrecognized.
func classifyByErrorCode(code string, err error) (string, bool) {
	switch code {
	case ErrorCodeRequestDisallowedByPolicy:
		msg := "Azure resource creation blocked by policy"
		policyName, assignmentID := ExtractPolicyDetails(err)
		switch {
		case policyName != "" && assignmentID != "":
			msg = fmt.Sprintf("Azure resource creation blocked by policy %q (assignment: %s)", policyName, assignmentID)
		case policyName != "":
			msg = fmt.Sprintf("Azure resource creation blocked by policy %q", policyName)
		case assignmentID != "":
			msg = fmt.Sprintf("Azure resource creation blocked by policy (assignment: %s)", assignmentID)
		}
		return msg, true
	case ErrorCodeQuotaExceeded:
		return "Azure quota exceeded", true
	case ErrorCodeOperationNotAllowed:
		// Non-429 OperationNotAllowed is a quota/limit issue, not throttling.
		return "Azure operation not allowed, check subscription quota and resource limits", true
	case ErrorCodeAllocationFailed:
		return "Azure allocation failed; requested resource capacity may not be available in the requested zone", true
	case ErrorCodeConflictingResourceName:
		return "Azure resource name conflict; purge soft-deleted resource or choose a different name", true
	case ErrorCodeRoleAssignmentExists:
		return "Azure role assignment already exists", true
	case ErrorCodeSkuNotAvailable:
		return "Azure SKU not available in the requested region or zone", true
	default:
		return "", false
	}
}

type armAdditionalInfo struct {
	Type string `json:"type"`
	Info struct {
		PolicyDefinitionDisplayName string `json:"policyDefinitionDisplayName"`
		PolicyDefinitionName        string `json:"policyDefinitionName"`
		PolicyAssignmentId          string `json:"policyAssignmentId"`
		PolicyAssignmentDisplayName string `json:"policyAssignmentDisplayName"`
		PolicyAssignmentName        string `json:"policyAssignmentName"`
	} `json:"info"`
}

func extractFromAdditionalInfo(entries []armAdditionalInfo) (policyName, assignmentID string) {
	for _, entry := range entries {
		if entry.Type != "PolicyViolation" {
			continue
		}
		policyName = entry.Info.PolicyDefinitionDisplayName
		if policyName == "" {
			policyName = entry.Info.PolicyDefinitionName
		}
		assignmentID = entry.Info.PolicyAssignmentId
		if assignmentID == "" {
			assignmentID = entry.Info.PolicyAssignmentName
		}
		if assignmentID == "" {
			assignmentID = entry.Info.PolicyAssignmentDisplayName
		}
		if policyName != "" || assignmentID != "" {
			return policyName, assignmentID
		}
	}
	return "", ""
}

// ExtractPolicyDetails parses a RequestDisallowedByPolicy error body to
// extract the policy name and assignment ID from ARM's PolicyViolation
// additionalInfo metadata (either top-level error.additionalInfo or nested
// under error.details[].additionalInfo). Returns empty strings on parse
// failure (defensive).
func ExtractPolicyDetails(err error) (policyName, assignmentID string) {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return "", ""
	}
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return "", ""
	}

	body, readErr := azruntime.Payload(respErr.RawResponse)
	if readErr != nil || len(body) == 0 {
		return "", ""
	}

	// ARM RequestDisallowedByPolicy error response structure:
	// { "error": { "code": "RequestDisallowedByPolicy", "additionalInfo": [ { "type": "PolicyViolation", "info": { "policyDefinitionDisplayName": "...", "policyAssignmentId": "..." } } ], "details": [ { "additionalInfo": [...] } ] } }
	var armResp struct {
		Error struct {
			AdditionalInfo []armAdditionalInfo `json:"additionalInfo"`
			Details        []struct {
				AdditionalInfo []armAdditionalInfo `json:"additionalInfo"`
			} `json:"details"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(body, &armResp); jsonErr != nil {
		return "", ""
	}

	if policyName, assignmentID = extractFromAdditionalInfo(armResp.Error.AdditionalInfo); policyName != "" || assignmentID != "" {
		return policyName, assignmentID
	}
	for _, detail := range armResp.Error.Details {
		if policyName, assignmentID = extractFromAdditionalInfo(detail.AdditionalInfo); policyName != "" || assignmentID != "" {
			return policyName, assignmentID
		}
	}

	return "", ""
}
