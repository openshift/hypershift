package resources

import (
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestConfigSyncError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ConfigSyncError
		expected string
	}{
		{
			name: "When error has wrapped error it should include it in message",
			err: &ConfigSyncError{
				Type:    ConfigSyncErrorInfrastructure,
				Message: "failed to update",
				Err:     errors.New("CEL validation failed"),
			},
			expected: "Infrastructure: failed to update: CEL validation failed",
		},
		{
			name: "When error has no wrapped error it should show type and message only",
			err: &ConfigSyncError{
				Type:    ConfigSyncErrorDNS,
				Message: "DNS config invalid",
				Err:     nil,
			},
			expected: "DNS: DNS config invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("ConfigSyncError.Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestConfigSyncError_Unwrap(t *testing.T) {
	t.Run("When error has wrapped error it should return it", func(t *testing.T) {
		wrappedErr := errors.New("underlying error")
		err := &ConfigSyncError{
			Type:    ConfigSyncErrorNetwork,
			Message: "network sync failed",
			Err:     wrappedErr,
		}

		if got := err.Unwrap(); got != wrappedErr {
			t.Errorf("ConfigSyncError.Unwrap() = %v, want %v", got, wrappedErr)
		}
	})

	t.Run("When error has no wrapped error it should return nil", func(t *testing.T) {
		err := &ConfigSyncError{
			Type:    ConfigSyncErrorNetwork,
			Message: "network sync failed",
			Err:     nil,
		}

		if got := err.Unwrap(); got != nil {
			t.Errorf("ConfigSyncError.Unwrap() = %v, want nil", got)
		}
	})
}

func TestConfigSyncErrors_Error(t *testing.T) {
	tests := []struct {
		name     string
		errs     *ConfigSyncErrors
		expected string
	}{
		{
			name:     "When no errors it should return empty string",
			errs:     &ConfigSyncErrors{},
			expected: "",
		},
		{
			name: "When single error it should return that error's message",
			errs: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorInfrastructure, Message: "infra failed"},
				},
			},
			expected: "Infrastructure: infra failed",
		},
		{
			name: "When multiple errors it should combine them",
			errs: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorInfrastructure, Message: "infra failed"},
					{Type: ConfigSyncErrorDNS, Message: "dns failed"},
				},
			},
			expected: "multiple config sync errors: [Infrastructure: infra failed; DNS: dns failed]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errs.Error(); got != tt.expected {
				t.Errorf("ConfigSyncErrors.Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestConfigSyncErrors_Add(t *testing.T) {
	t.Run("When adding errors it should accumulate them", func(t *testing.T) {
		errs := &ConfigSyncErrors{}

		errs.Add(ConfigSyncErrorInfrastructure, "infra failed", nil)
		if len(errs.Errors) != 1 {
			t.Errorf("Expected 1 error, got %d", len(errs.Errors))
		}

		errs.Add(ConfigSyncErrorDNS, "dns failed", errors.New("underlying"))
		if len(errs.Errors) != 2 {
			t.Errorf("Expected 2 errors, got %d", len(errs.Errors))
		}

		if errs.Errors[0].Type != ConfigSyncErrorInfrastructure {
			t.Errorf("First error type = %s, want %s", errs.Errors[0].Type, ConfigSyncErrorInfrastructure)
		}
		if errs.Errors[1].Type != ConfigSyncErrorDNS {
			t.Errorf("Second error type = %s, want %s", errs.Errors[1].Type, ConfigSyncErrorDNS)
		}
	})
}

func TestConfigSyncErrors_HasErrors(t *testing.T) {
	tests := []struct {
		name     string
		errs     *ConfigSyncErrors
		expected bool
	}{
		{
			name:     "When no errors it should return false",
			errs:     &ConfigSyncErrors{},
			expected: false,
		},
		{
			name: "When has errors it should return true",
			errs: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorInfrastructure, Message: "failed"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errs.HasErrors(); got != tt.expected {
				t.Errorf("ConfigSyncErrors.HasErrors() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConfigSyncErrors_ToError(t *testing.T) {
	t.Run("When no errors it should return nil", func(t *testing.T) {
		errs := &ConfigSyncErrors{}
		if got := errs.ToError(); got != nil {
			t.Errorf("ConfigSyncErrors.ToError() = %v, want nil", got)
		}
	})

	t.Run("When has errors it should return self", func(t *testing.T) {
		errs := &ConfigSyncErrors{
			Errors: []*ConfigSyncError{
				{Type: ConfigSyncErrorInfrastructure, Message: "failed"},
			},
		}
		got := errs.ToError()
		if got != errs {
			t.Errorf("ConfigSyncErrors.ToError() should return self")
		}
	})
}

func TestDetermineConfigSyncReason(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "When nil error it should return ConfigSyncedReason",
			err:      nil,
			expected: hyperv1.ConfigSyncedReason,
		},
		{
			name: "When single infrastructure error it should return InfrastructureSyncFailedReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorInfrastructure, Message: "failed"},
				},
			},
			expected: hyperv1.InfrastructureSyncFailedReason,
		},
		{
			name: "When single DNS error it should return DNSSyncFailedReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorDNS, Message: "failed"},
				},
			},
			expected: hyperv1.DNSSyncFailedReason,
		},
		{
			name: "When single Ingress error it should return IngressSyncFailedReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorIngress, Message: "failed"},
				},
			},
			expected: hyperv1.IngressSyncFailedReason,
		},
		{
			name: "When single Network error it should return NetworkSyncFailedReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorNetwork, Message: "failed"},
				},
			},
			expected: hyperv1.NetworkSyncFailedReason,
		},
		{
			name: "When single Other error it should return ReconcileErrorReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorOther, Message: "failed"},
				},
			},
			expected: hyperv1.ReconcileErrorReason,
		},
		{
			name: "When multiple errors it should return MultipleConfigSyncFailedReason",
			err: &ConfigSyncErrors{
				Errors: []*ConfigSyncError{
					{Type: ConfigSyncErrorInfrastructure, Message: "infra failed"},
					{Type: ConfigSyncErrorDNS, Message: "dns failed"},
				},
			},
			expected: hyperv1.MultipleConfigSyncFailedReason,
		},
		{
			name:     "When non-ConfigSyncErrors error it should return ReconcileErrorReason",
			err:      errors.New("some random error"),
			expected: hyperv1.ReconcileErrorReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := determineConfigSyncReason(tt.err); got != tt.expected {
				t.Errorf("determineConfigSyncReason() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestConfigSyncErrors_ErrorsAs(t *testing.T) {
	t.Run("When using errors.As it should unwrap ConfigSyncErrors", func(t *testing.T) {
		original := &ConfigSyncErrors{
			Errors: []*ConfigSyncError{
				{Type: ConfigSyncErrorInfrastructure, Message: "failed"},
			},
		}

		var target *ConfigSyncErrors
		if !errors.As(original, &target) {
			t.Error("errors.As should return true for ConfigSyncErrors")
		}
		if target != original {
			t.Error("errors.As should set target to original")
		}
	})
}
