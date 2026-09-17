package gcp

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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
			msg := fmt.Sprintf("%s: bad request (400): %s", context, googleErr.Message)
			m.logger.Info("WARNING: " + msg)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg)
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
