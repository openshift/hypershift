package gcp

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
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

func TestDestroyIAMOptionsValidateResourceReferences(t *testing.T) {
	validOptions := func() *DestroyIAMOptions {
		return &DestroyIAMOptions{
			ProjectID:                     "test-project-id",
			WorkloadIdentityProjectNumber: "987654321",
			PoolID:                        "recorded-pool",
			ProviderID:                    "recorded-provider",
			ServiceAccountEmails: map[string]string{
				"nodepool-mgmt":    "nodepool@test-project.iam.gserviceaccount.com",
				"ctrlplane-op":     "controlplane@test-project.iam.gserviceaccount.com",
				"cloud-controller": "controller@test-project.iam.gserviceaccount.com",
				"gcp-pd-csi":       "storage@test-project.iam.gserviceaccount.com",
				"image-registry":   "registry@test-project.iam.gserviceaccount.com",
				"cloud-network":    "network@test-project.iam.gserviceaccount.com",
			},
		}
	}

	if err := validOptions().ValidateResourceReferences(); err != nil {
		t.Fatalf("expected exact HostedCluster references to validate: %v", err)
	}
	for _, test := range []struct{ name, projectNumber, errorText string }{
		{"When the WIF project number is missing, it should reject IAM cleanup", "", "workload identity project number is required"},
		{"When the WIF project number is invalid, it should reject IAM cleanup", "other-project", "workload identity project number must contain only digits"},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := validOptions()
			opts.WorkloadIdentityProjectNumber = test.projectNumber
			if err := opts.ValidateResourceReferences(); err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected error containing %q, got %v", test.errorText, err)
			}
		})
	}

	missingPool := validOptions()
	missingPool.PoolID = ""
	if err := missingPool.ValidateResourceReferences(); err == nil || !strings.Contains(err.Error(), "workload identity pool ID is required") {
		t.Fatalf("expected missing pool reference error, got %v", err)
	}

	missingAccount := validOptions()
	delete(missingAccount.ServiceAccountEmails, "image-registry")
	if err := missingAccount.ValidateResourceReferences(); err == nil || !strings.Contains(err.Error(), "service account email for image-registry is required") {
		t.Fatalf("expected missing service account reference error, got %v", err)
	}
}

func TestDestroyIAMOptionsDestroyIAM(t *testing.T) {
	for _, test := range []struct {
		name, errorText string
		opts            DestroyIAMOptions
	}{
		{
			name:      "When only the WIF project is explicit, it should reject incomplete references instead of using InfraID",
			opts:      DestroyIAMOptions{ProjectID: "test-project", InfraID: "test-infra", WorkloadIdentityProjectNumber: "987654321"},
			errorText: "workload identity pool ID is required",
		},
		{
			name:      "When explicit references lack a WIF project number, it should reject cleanup before creating clients",
			opts:      DestroyIAMOptions{ProjectID: "test-project", PoolID: "recorded-pool", ProviderID: "recorded-provider"},
			errorText: "workload identity project number is required",
		},
		{
			name: "When standalone cleanup lacks InfraID, it should retain its existing validation",
			opts: DestroyIAMOptions{ProjectID: "test-project"}, errorText: "infra-id is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.opts.DestroyIAM(context.Background(), logr.Discard())
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected error containing %q, got %v", test.errorText, err)
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
