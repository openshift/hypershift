package resources

import (
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// ConfigSyncErrorType represents the type of config that failed to sync.
type ConfigSyncErrorType string

const (
	ConfigSyncErrorInfrastructure ConfigSyncErrorType = "Infrastructure"
	ConfigSyncErrorDNS            ConfigSyncErrorType = "DNS"
	ConfigSyncErrorIngress        ConfigSyncErrorType = "Ingress"
	ConfigSyncErrorNetwork        ConfigSyncErrorType = "Network"
	ConfigSyncErrorImage          ConfigSyncErrorType = "Image"
	ConfigSyncErrorProxy          ConfigSyncErrorType = "Proxy"
	ConfigSyncErrorAuthentication ConfigSyncErrorType = "Authentication"
	ConfigSyncErrorAPIServer      ConfigSyncErrorType = "APIServer"
	ConfigSyncErrorOther          ConfigSyncErrorType = "Other"
)

// ConfigSyncError represents an error that occurred during config sync.
type ConfigSyncError struct {
	Type    ConfigSyncErrorType
	Message string
	Err     error
}

func (e *ConfigSyncError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

func (e *ConfigSyncError) Unwrap() error {
	return e.Err
}

// ConfigSyncErrors is a collection of ConfigSyncError that occurred during reconciliation.
type ConfigSyncErrors struct {
	Errors []*ConfigSyncError
}

func (e *ConfigSyncErrors) Error() string {
	if len(e.Errors) == 0 {
		return ""
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var msgs []string
	for _, err := range e.Errors {
		msgs = append(msgs, err.Error())
	}
	return fmt.Sprintf("multiple config sync errors: [%s]", strings.Join(msgs, "; "))
}

func (e *ConfigSyncErrors) Add(errType ConfigSyncErrorType, message string, err error) {
	e.Errors = append(e.Errors, &ConfigSyncError{
		Type:    errType,
		Message: message,
		Err:     err,
	})
}

func (e *ConfigSyncErrors) HasErrors() bool {
	return len(e.Errors) > 0
}

// ToError returns nil if no errors, or the ConfigSyncErrors if there are errors.
func (e *ConfigSyncErrors) ToError() error {
	if !e.HasErrors() {
		return nil
	}
	return e
}

// determineConfigSyncReason analyzes the error from reconcileConfig and returns
// the appropriate reason for the HostedClusterConfigSynced condition.
func determineConfigSyncReason(err error) string {
	if err == nil {
		return hyperv1.ConfigSyncedReason
	}

	var configErrs *ConfigSyncErrors
	if errors.As(err, &configErrs) {
		if len(configErrs.Errors) > 1 {
			return hyperv1.MultipleConfigSyncFailedReason
		}
		if len(configErrs.Errors) == 1 {
			switch configErrs.Errors[0].Type {
			case ConfigSyncErrorInfrastructure:
				return hyperv1.InfrastructureSyncFailedReason
			case ConfigSyncErrorDNS:
				return hyperv1.DNSSyncFailedReason
			case ConfigSyncErrorIngress:
				return hyperv1.IngressSyncFailedReason
			case ConfigSyncErrorNetwork:
				return hyperv1.NetworkSyncFailedReason
			default:
				return hyperv1.ReconcileErrorReason
			}
		}
	}

	// Fallback for non-typed errors
	return hyperv1.ReconcileErrorReason
}
