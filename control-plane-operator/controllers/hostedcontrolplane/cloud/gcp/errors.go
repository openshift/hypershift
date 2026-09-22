package gcp

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// classify maps a GCP API error to a Result following the "degrade, don't wedge"
// contract. Auth/permission/rate-limit/conflict/bad-request codes are treated as
// expected, recoverable states (OutcomeDegraded); anything else is an
// unexpected OutcomeError.
//
// This mirrors the PSC controller's handleGCPError code classification
// (403/429/409/400).
func (m *FirewallManager) classify(err error, context string) Result {
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		switch googleErr.Code {
		case 403:
			msg := fmt.Sprintf("%s: permission denied (403). The ctrlplane-op service account likely needs roles/compute.securityAdmin: %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallInsufficientPermissions, msg)
		case 429:
			msg := fmt.Sprintf("%s: rate limited (429), will retry: %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
		case 409:
			msg := fmt.Sprintf("%s: resource conflict (409), will retry: %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
		case 404:
			// A missing VPC is a recoverable infra state, not a hard error.
			msg := fmt.Sprintf("%s: resource not found (404): %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
		case 400:
			// A 400 can be transient (a referenced resource is not ready yet) or
			// terminal (a malformed/invalid request that cannot succeed on retry).
			// Only known-transient reasons are treated as waiting; everything else
			// surfaces as an actionable invalid-configuration state instead of being
			// masked as waiting-for-infra forever.
			if isTransientBadRequest(err) {
				msg := fmt.Sprintf("%s: bad request (400, transient), will retry: %s", context, googleErr.Message)
				m.logger.Info("WARNING: " + msg)
				return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
			}
			msg := fmt.Sprintf("%s: invalid request (400): %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallInvalidConfiguration, msg)
		}
	}
	return errorResult(hyperv1.GCPFirewallWaitingForInfra, fmt.Errorf("%s: %w", context, err))
}

// classifyOpError maps a failed global operation to a Result. Operation errors
// carry per-error codes (e.g. PERMISSION_DENIED, QUOTA_EXCEEDED) rather than
// HTTP status, so classification is based on those codes.
func (m *FirewallManager) classifyOpError(op *compute.Operation, action string) Result {
	msg := fmt.Sprintf("firewall %s operation failed: %s", action, formatOperationErrors(op.Error.Errors))
	for _, e := range op.Error.Errors {
		switch e.Code {
		case "PERMISSION_DENIED", "FORBIDDEN":
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallInsufficientPermissions, msg)
		case "QUOTA_EXCEEDED", "RATE_LIMIT_EXCEEDED", "RESOURCE_NOT_READY":
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
		}
	}
	return errorResult(hyperv1.GCPFirewallWaitingForInfra, errors.New(msg))
}

// transientBadRequestReasons are GCP Compute 400 error reasons that can
// plausibly succeed on a later retry (a referenced resource is still being
// created or is momentarily in use by another operation). Any 400 not in this
// set is treated as a terminal invalid-configuration error.
//
// The Compute Engine API returns the error reason as a free-form string, not a
// typed enum: google.rpc.ErrorInfo.reason is an open, per-service namespace
// (regex-constrained UPPER_SNAKE_CASE, and "not a complete list" per the error
// catalog at https://cloud.google.com/compute/docs/reference/rest/v1/errors), so
// no Go SDK surface exposes it as a constant — not googleapi.ErrorItem.Reason,
// compute.ErrorInfo.Reason, errdetails.ErrorInfo.Reason, nor apierror.APIError.
// Reason(). Matching known-transient reasons against a curated set is therefore
// the intended integration pattern.
//
// Reasons are stored and matched in a separator-stripped, lowercase form (see
// normalizeReason) so the UPPER_SNAKE_CASE spelling from the error catalog /
// structured ErrorInfo (e.g. "RESOURCE_NOT_READY") and the lowerCamelCase
// spelling from the legacy Compute v1 REST error.errors[].reason field (e.g.
// "resourceNotReady") both match the same entry.
var transientBadRequestReasons = map[string]struct{}{
	normalizeReason("RESOURCE_NOT_READY"):                  {},
	normalizeReason("RESOURCE_IN_USE_BY_ANOTHER_RESOURCE"): {},
}

// isTransientBadRequest reports whether a 400 error can plausibly succeed on
// retry. A response may carry several reasons; it is only transient when at
// least one reason is present and every reason is transient. A single terminal
// reason (e.g. an invalid field) means the request cannot succeed on retry, so a
// mixed transient+terminal response is classified terminal rather than being
// masked as waiting-for-infra.
func isTransientBadRequest(err error) bool {
	reasons := errorReasons(err)
	if len(reasons) == 0 {
		return false
	}
	for _, reason := range reasons {
		if _, ok := transientBadRequestReasons[normalizeReason(reason)]; !ok {
			return false
		}
	}
	return true
}

// errorReasons extracts the machine-readable reason strings describing a GCP API
// error. The typed ErrorInfo.Reason exposed by apierror.APIError (which the
// compute/v1 REST client wires up) and the legacy free-form
// googleapi.Error.Errors[].Reason describe the *same* error, so they are not
// unioned: when the structured reason is present it is authoritative and is
// returned alone, otherwise every legacy reason item is returned. The compute
// backend does not always emit a structured ErrorInfo, hence the fallback.
func errorReasons(err error) []string {
	var apiErr *apierror.APIError
	if errors.As(err, &apiErr) {
		if r := apiErr.Reason(); r != "" {
			return []string{r}
		}
	}
	var reasons []string
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		for _, e := range googleErr.Errors {
			if e.Reason != "" {
				reasons = append(reasons, e.Reason)
			}
		}
	}
	return reasons
}

// normalizeReason reduces a reason to a lowercase, separator-free form so the
// UPPER_SNAKE_CASE, camelCase, and hyphenated spellings of the same reason all
// compare equal (e.g. "RESOURCE_NOT_READY" and "resourceNotReady" both become
// "resourcenotready").
func normalizeReason(reason string) string {
	var b strings.Builder
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isNotFoundError reports whether err is a GCP 404.
func isNotFoundError(err error) bool {
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		return googleErr.Code == 404
	}
	return false
}

// isAlreadyExistsError reports whether err is a GCP 409 conflict (already exists).
func isAlreadyExistsError(err error) bool {
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		return googleErr.Code == 409
	}
	return false
}

// formatOperationErrors renders GCP operation errors into a readable string.
func formatOperationErrors(errs []*compute.OperationErrorErrors) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		messages = append(messages, fmt.Sprintf("%s: %s", e.Code, e.Message))
	}
	return fmt.Sprintf("[%s]", strings.Join(messages, ", "))
}
