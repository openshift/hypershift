package gcp

import (
	"strings"
	"testing"
)

func TestDestroyIAMOptionsValidateInputs(t *testing.T) {
	tests := []struct {
		name          string
		opts          *DestroyIAMOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &DestroyIAMOptions{
				InfraID:   "test-infra-id",
				ProjectID: "test-project-id",
			},
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &DestroyIAMOptions{
				InfraID:   "",
				ProjectID: "test-project-id",
			},
			expectedError: "infra-id is required",
		},
		{
			name: "When project-id is missing it should return error",
			opts: &DestroyIAMOptions{
				InfraID:   "test-infra-id",
				ProjectID: "",
			},
			expectedError: "project-id is required",
		},
		{
			name: "When both infra-id and project-id are missing it should return infra-id error first",
			opts: &DestroyIAMOptions{
				InfraID:   "",
				ProjectID: "",
			},
			expectedError: "infra-id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.ValidateInputs()

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestDestroyIAMNewDestroyIAMCommand(t *testing.T) {
	cmd := NewDestroyIAMCommand()

	if cmd == nil {
		t.Fatal("expected command to be non-nil")
		return
	}

	if cmd.Use != "gcp" {
		t.Errorf("expected Use to be %q, got %q", "gcp", cmd.Use)
	}

	// Verify required flags are defined
	infraIDFlag := cmd.Flag("infra-id")
	if infraIDFlag == nil {
		t.Error("expected infra-id flag to be defined")
	}

	projectIDFlag := cmd.Flag("project-id")
	if projectIDFlag == nil {
		t.Error("expected project-id flag to be defined")
	}
}
