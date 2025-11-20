package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestValidateInputs(t *testing.T) {
	tests := []struct {
		name          string
		opts          *CreateIAMOptions
		setupJWKSFile bool
		jwksContent   string
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &CreateIAMOptions{
				InfraID:   "test-infra-id",
				ProjectID: "test-project-id",
			},
			setupJWKSFile: true,
			jwksContent:   `{"keys": [{"kty": "RSA", "use": "sig", "kid": "test-key"}]}`,
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &CreateIAMOptions{
				InfraID:   "",
				ProjectID: "test-project-id",
			},
			setupJWKSFile: true,
			jwksContent:   `{"keys": [{"kty": "RSA"}]}`,
			expectedError: "infra-id is required",
		},
		{
			name: "When project-id is missing it should return error",
			opts: &CreateIAMOptions{
				InfraID:   "test-infra-id",
				ProjectID: "",
			},
			setupJWKSFile: true,
			jwksContent:   `{"keys": [{"kty": "RSA"}]}`,
			expectedError: "project-id is required",
		},
		{
			name: "When oidc-jwks-file is missing it should return error",
			opts: &CreateIAMOptions{
				InfraID:             "test-infra-id",
				ProjectID:           "test-project-id",
				ClusterOIDCJWKSFile: "",
			},
			setupJWKSFile: false,
			expectedError: "oidc-jwks-file is required",
		},
		{
			name: "When JWKS validation fails it should return error",
			opts: &CreateIAMOptions{
				InfraID:             "test-infra-id",
				ProjectID:           "test-project-id",
				ClusterOIDCJWKSFile: "non-existent-file",
			},
			setupJWKSFile: false,
			expectedError: "invalid JWKS file",
		},
		{
			name: "When all fields including optional OIDCIssuerURL are provided it should pass validation",
			opts: &CreateIAMOptions{
				InfraID:       "test-infra-id",
				ProjectID:     "test-project-id",
				OIDCIssuerURL: "https://custom-oidc.example.com",
			},
			setupJWKSFile: true,
			jwksContent:   `{"keys": [{"kty": "RSA", "use": "sig", "kid": "test-key"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			if tt.setupJWKSFile {
				jwksFile := filepath.Join(tmpDir, "jwks.json")
				if err := os.WriteFile(jwksFile, []byte(tt.jwksContent), 0644); err != nil {
					t.Fatalf("failed to create test JWKS file: %v", err)
				}
				tt.opts.ClusterOIDCJWKSFile = jwksFile
			}

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

func TestOutput(t *testing.T) {
	tests := []struct {
		name          string
		outputFile    string
		results       *CreateIAMOutput
		expectedError string
		validateJSON  bool
	}{
		{
			name:       "When output file is specified it should write JSON to file",
			outputFile: "output.json",
			results: &CreateIAMOutput{
				ProjectID:     "test-project",
				ProjectNumber: "123456789",
				InfraID:       "test-infra",
				WorkloadIdentityPool: WorkloadIdentityConfig{
					PoolID:     "test-pool",
					ProviderID: "test-provider",
					Audience:   "//iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-pool/providers/test-provider",
				},
			},
			validateJSON: true,
		},
		{
			name:       "When output file is in invalid directory it should return error",
			outputFile: "/nonexistent/directory/output.json",
			results: &CreateIAMOutput{
				ProjectID: "test-project",
			},
			expectedError: "cannot create output file",
		},
		{
			name:       "When output file is empty string it should write to stdout without error",
			outputFile: "",
			results: &CreateIAMOutput{
				ProjectID:     "test-project",
				ProjectNumber: "123456789",
				InfraID:       "test-infra",
			},
		},
		{
			name:       "When results contain all fields it should serialize correctly",
			outputFile: "complete-output.json",
			results: &CreateIAMOutput{
				ProjectID:     "my-gcp-project",
				ProjectNumber: "987654321",
				InfraID:       "my-cluster-infra",
				WorkloadIdentityPool: WorkloadIdentityConfig{
					PoolID:     "my-cluster-infra-wi-pool",
					ProviderID: "my-cluster-infra-k8s-provider",
					Audience:   "//iam.googleapis.com/projects/987654321/locations/global/workloadIdentityPools/my-cluster-infra-wi-pool/providers/my-cluster-infra-k8s-provider",
				},
				ServiceAccounts: map[string]string{
					"nodepool-mgmt": "my-cluster-infra-nodepool-mgmt@my-gcp-project.iam.gserviceaccount.com",
					"ctrlplane-op":  "my-cluster-infra-ctrlplane-op@my-gcp-project.iam.gserviceaccount.com",
				},
			},
			validateJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			var outputPath string

			if tt.outputFile != "" && !filepath.IsAbs(tt.outputFile) {
				outputPath = filepath.Join(tmpDir, tt.outputFile)
			} else {
				outputPath = tt.outputFile
			}

			opts := &CreateIAMOptions{
				OutputFile: outputPath,
			}

			logger := logr.Discard()
			err := opts.Output(tt.results, logger)

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("expected no error, got %v", err)
				return
			}

			if tt.validateJSON && outputPath != "" {
				// Read the file and validate it's valid JSON
				data, err := os.ReadFile(outputPath)
				if err != nil {
					t.Fatalf("failed to read output file: %v", err)
				}

				// Unmarshal to validate JSON structure
				var output CreateIAMOutput
				if err := json.Unmarshal(data, &output); err != nil {
					t.Errorf("output is not valid JSON: %v", err)
					return
				}

				// Validate content matches
				if output.ProjectID != tt.results.ProjectID {
					t.Errorf("expected ProjectID %q, got %q", tt.results.ProjectID, output.ProjectID)
				}
				if output.ProjectNumber != tt.results.ProjectNumber {
					t.Errorf("expected ProjectNumber %q, got %q", tt.results.ProjectNumber, output.ProjectNumber)
				}
				if output.InfraID != tt.results.InfraID {
					t.Errorf("expected InfraID %q, got %q", tt.results.InfraID, output.InfraID)
				}
				if output.WorkloadIdentityPool.PoolID != tt.results.WorkloadIdentityPool.PoolID {
					t.Errorf("expected PoolID %q, got %q", tt.results.WorkloadIdentityPool.PoolID, output.WorkloadIdentityPool.PoolID)
				}
				if output.WorkloadIdentityPool.ProviderID != tt.results.WorkloadIdentityPool.ProviderID {
					t.Errorf("expected ProviderID %q, got %q", tt.results.WorkloadIdentityPool.ProviderID, output.WorkloadIdentityPool.ProviderID)
				}
				if output.WorkloadIdentityPool.Audience != tt.results.WorkloadIdentityPool.Audience {
					t.Errorf("expected Audience %q, got %q", tt.results.WorkloadIdentityPool.Audience, output.WorkloadIdentityPool.Audience)
				}

				// Validate ServiceAccounts if present
				if tt.results.ServiceAccounts != nil {
					if output.ServiceAccounts == nil {
						t.Errorf("expected ServiceAccounts to be non-nil")
					} else if len(output.ServiceAccounts) != len(tt.results.ServiceAccounts) {
						t.Errorf("expected %d service accounts, got %d", len(tt.results.ServiceAccounts), len(output.ServiceAccounts))
					} else {
						for name, expectedEmail := range tt.results.ServiceAccounts {
							if actualEmail, exists := output.ServiceAccounts[name]; !exists {
								t.Errorf("expected service account %q to exist", name)
							} else if actualEmail != expectedEmail {
								t.Errorf("expected service account %q to have email %q, got %q", name, expectedEmail, actualEmail)
							}
						}
					}
				}
			}
		})
	}
}

func TestValidateJWKSFile(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		setupFile     bool
		expectedError string
	}{
		{
			name:        "When valid JWKS file is provided it should pass validation",
			fileContent: `{"keys": [{"kty": "RSA", "use": "sig", "kid": "test-key"}]}`,
			setupFile:   true,
		},
		{
			name:          "When file does not exist it should return file not exist error",
			setupFile:     false,
			expectedError: "cluster OIDC JWKS file does not exist",
		},
		{
			name:          "When file contains invalid JSON it should return invalid JSON error",
			fileContent:   `{invalid json}`,
			setupFile:     true,
			expectedError: "JWKS file contains invalid JSON",
		},
		{
			name:          "When file is valid JSON but missing keys field it should return error",
			fileContent:   `{"other": "field"}`,
			setupFile:     true,
			expectedError: "JWKS file must contain a 'keys' field",
		},
		{
			name:          "When file is empty JSON object it should return error",
			fileContent:   `{}`,
			setupFile:     true,
			expectedError: "JWKS file must contain a 'keys' field",
		},
		{
			name:        "When JWKS has multiple keys it should pass validation",
			fileContent: `{"keys": [{"kty": "RSA", "kid": "key1"}, {"kty": "RSA", "kid": "key2"}]}`,
			setupFile:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var testFile string
			if tt.setupFile {
				// Create a temporary file
				tmpDir := t.TempDir()
				testFile = filepath.Join(tmpDir, "jwks.json")
				if err := os.WriteFile(testFile, []byte(tt.fileContent), 0644); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
			} else {
				// Use a non-existent file path
				testFile = filepath.Join(t.TempDir(), "non-existent.json")
			}

			opts := &CreateIAMOptions{
				ClusterOIDCJWKSFile: testFile,
			}

			err := opts.ValidateJWKSFile()

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if err.Error() == "" || !strings.Contains(err.Error(), tt.expectedError) {
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
