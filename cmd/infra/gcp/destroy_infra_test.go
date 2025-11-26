package gcp

import (
	"strings"
	"testing"

	"google.golang.org/api/compute/v1"
)

func TestDestroyInfraOptionsValidate(t *testing.T) {
	tests := []struct {
		name          string
		opts          *DestroyInfraOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
		},
		{
			name: "When project-id is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
			expectedError: "--project-id is required",
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "",
				Region:    "us-central1",
			},
			expectedError: "--infra-id is required",
		},
		{
			name: "When region is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "",
			},
			expectedError: "--region is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()

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

func TestFormatOperationErrors(t *testing.T) {
	tests := []struct {
		name     string
		errors   []*compute.OperationErrorErrors
		expected string
	}{
		{
			name:     "When errors is nil it should return unknown error",
			errors:   nil,
			expected: "unknown error",
		},
		{
			name:     "When errors is empty it should return unknown error",
			errors:   []*compute.OperationErrorErrors{},
			expected: "unknown error",
		},
		{
			name: "When single error it should format correctly",
			errors: []*compute.OperationErrorErrors{
				{Code: "RESOURCE_IN_USE", Message: "Resource is in use"},
			},
			expected: "[RESOURCE_IN_USE: Resource is in use]",
		},
		{
			name: "When multiple errors it should format all",
			errors: []*compute.OperationErrorErrors{
				{Code: "ERROR_1", Message: "First error"},
				{Code: "ERROR_2", Message: "Second error"},
			},
			expected: "[ERROR_1: First error, ERROR_2: Second error]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatOperationErrors(tt.errors)
			if got != tt.expected {
				t.Errorf("formatOperationErrors() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name     string
		strs     []string
		sep      string
		expected string
	}{
		{
			name:     "When empty slice it should return empty string",
			strs:     []string{},
			sep:      ", ",
			expected: "",
		},
		{
			name:     "When single element it should return element",
			strs:     []string{"one"},
			sep:      ", ",
			expected: "one",
		},
		{
			name:     "When multiple elements it should join with separator",
			strs:     []string{"one", "two", "three"},
			sep:      ", ",
			expected: "one, two, three",
		},
		{
			name:     "When different separator it should use it",
			strs:     []string{"a", "b"},
			sep:      " | ",
			expected: "a | b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinStrings(tt.strs, tt.sep)
			if got != tt.expected {
				t.Errorf("joinStrings() = %q, want %q", got, tt.expected)
			}
		})
	}
}
