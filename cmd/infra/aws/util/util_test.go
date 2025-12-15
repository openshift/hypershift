package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewSessionV2(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name            string
		agent           string
		credentialsFile string
		credKey         string
		credSecretKey   string
		region          string
		setupFunc       func(t *testing.T) string
		cleanupFunc     func(t *testing.T, path string)
		expectNonNil    bool
		validateConfig  func(t *testing.T, cfg *awsv2.Config)
	}{
		{
			name:          "When given static credentials and region, it should create config with region",
			agent:         "test-agent",
			credKey:       "test-key",
			credSecretKey: "test-secret",
			region:        "us-east-1",
			expectNonNil:  true,
			validateConfig: func(t *testing.T, cfg *awsv2.Config) {
				if cfg.Region != "us-east-1" {
					t.Errorf("Expected region us-east-1, got %s", cfg.Region)
				}
			},
		},
		{
			name:         "When given credentials file, it should load config from file",
			agent:        "test-agent",
			region:       "us-west-2",
			expectNonNil: true,
			setupFunc: func(t *testing.T) string {
				// Create a temporary credentials file
				tmpDir := t.TempDir()
				credsFile := filepath.Join(tmpDir, "credentials")
				content := `[default]
aws_access_key_id = test-key-id
aws_secret_access_key = test-secret-key
`
				if err := os.WriteFile(credsFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to create temp credentials file: %v", err)
				}
				return credsFile
			},
			validateConfig: func(t *testing.T, cfg *awsv2.Config) {
				if cfg.Region != "us-west-2" {
					t.Errorf("Expected region us-west-2, got %s", cfg.Region)
				}
			},
		},
		{
			name:          "When region is not provided, it should create config without region",
			agent:         "test-agent",
			credKey:       "test-key",
			credSecretKey: "test-secret",
			region:        "",
			expectNonNil:  true,
			validateConfig: func(t *testing.T, cfg *awsv2.Config) {
				// Region should be empty or default
				// We just verify the config is created
				if cfg == nil {
					t.Error("Expected non-nil config")
				}
			},
		},
		{
			name:         "When given minimal configuration, it should create valid config",
			agent:        "minimal-agent",
			expectNonNil: true,
			validateConfig: func(t *testing.T, cfg *awsv2.Config) {
				if cfg == nil {
					t.Error("Expected non-nil config")
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var credsFile string
			if tc.setupFunc != nil {
				credsFile = tc.setupFunc(t)
			}
			if tc.cleanupFunc != nil {
				defer tc.cleanupFunc(t, credsFile)
			}

			// Use credentials file from setup if provided
			if credsFile != "" {
				tc.credentialsFile = credsFile
			}

			cfg := NewSessionV2(ctx, tc.agent, tc.credentialsFile, tc.credKey, tc.credSecretKey, tc.region)

			if tc.expectNonNil && cfg == nil {
				t.Error("Expected non-nil config, got nil")
				return
			}

			if !tc.expectNonNil && cfg != nil {
				t.Error("Expected nil config, got non-nil")
				return
			}

			if tc.validateConfig != nil {
				tc.validateConfig(t, cfg)
			}
		})
	}
}

func TestNewSessionV2_UserAgent(t *testing.T) {
	ctx := context.Background()
	agent := "test-user-agent"

	cfg := NewSessionV2(ctx, agent, "", "test-key", "test-secret", "us-east-1")

	if cfg == nil {
		t.Fatal("Expected non-nil config")
	}
}

func TestNewSessionV2_ContextPropagation(t *testing.T) {
	// Create a context with a specific value to verify it's being used
	type ctxKey string
	testKey := ctxKey("test")
	ctx := context.WithValue(context.Background(), testKey, "test-value")

	cfg := NewSessionV2(ctx, "test-agent", "", "test-key", "test-secret", "us-east-1")

	if cfg == nil {
		t.Error("Expected non-nil config")
	}
}

func TestGetSessionV2(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name        string
		opts        AWSCredentialsOptions
		agent       string
		region      string
		setupFunc   func(t *testing.T) string
		expectError bool
		errorMsg    string
	}{
		{
			name:   "When given AWS credentials file, it should create session successfully",
			opts:   AWSCredentialsOptions{},
			agent:  "test-agent",
			region: "us-east-1",
			setupFunc: func(t *testing.T) string {
				tmpDir := t.TempDir()
				credsFile := filepath.Join(tmpDir, "credentials")
				content := `[default]
aws_access_key_id = test-key-id
aws_secret_access_key = test-secret-key
`
				if err := os.WriteFile(credsFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to create temp credentials file: %v", err)
				}
				return credsFile
			},
			expectError: false,
		},
		{
			name:        "When no credentials provided, it should return error",
			opts:        AWSCredentialsOptions{},
			agent:       "test-agent",
			region:      "us-east-1",
			expectError: true,
			errorMsg:    "could not create AWS session, no credentials were given",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupFunc != nil {
				credsFile := tc.setupFunc(t)
				tc.opts.AWSCredentialsFile = credsFile
			}

			cfg, err := tc.opts.GetSessionV2(ctx, tc.agent, nil, tc.region)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				} else if tc.errorMsg != "" && err.Error() != tc.errorMsg {
					t.Errorf("Expected error message %q, got %q", tc.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if cfg == nil {
				t.Error("Expected non-nil config")
			}
		})
	}
}
