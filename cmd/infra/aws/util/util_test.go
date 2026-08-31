package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewSession(t *testing.T) {
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
		validateConfig  func(t *testing.T, cfg *aws.Config)
	}{
		{
			name:          "When given static credentials and region, it should create config with region",
			agent:         "test-agent",
			credKey:       "test-key",
			credSecretKey: "test-secret",
			region:        "us-east-1",
			expectNonNil:  true,
			validateConfig: func(t *testing.T, cfg *aws.Config) {
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
				for _, env := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
					t.Setenv(env, "")
				}
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
			validateConfig: func(t *testing.T, cfg *aws.Config) {
				if cfg.Region != "us-west-2" {
					t.Errorf("Expected region us-west-2, got %s", cfg.Region)
				}
			},
		},
		{
			// Regression test: --aws-creds file must be used as both a shared config file
			// and a shared credentials file. Previously only WithSharedConfigFiles was set,
			// so credentials were never read from the provided file and fell back to
			// AWS_* env vars or ~/.aws/credentials.
			name:         "When given credentials file, it should read credentials from the file not from env vars",
			agent:        "test-agent",
			region:       "us-east-1",
			expectNonNil: true,
			setupFunc: func(t *testing.T) string {
				// Ensure AWS env vars don't shadow the file credentials.
				for _, env := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
					t.Setenv(env, "")
				}
				tmpDir := t.TempDir()
				credsFile := filepath.Join(tmpDir, "credentials")
				content := `[default]
aws_access_key_id = file-access-key
aws_secret_access_key = file-secret-key
`
				if err := os.WriteFile(credsFile, []byte(content), 0600); err != nil {
					t.Fatalf("Failed to create temp credentials file: %v", err)
				}
				return credsFile
			},
			validateConfig: func(t *testing.T, cfg *aws.Config) {
				creds, err := cfg.Credentials.Retrieve(context.Background())
				if err != nil {
					t.Fatalf("Failed to retrieve credentials: %v", err)
				}
				if creds.AccessKeyID != "file-access-key" {
					t.Errorf("Expected AccessKeyID %q from file, got %q — credentials were not loaded from the provided file", "file-access-key", creds.AccessKeyID)
				}
				if creds.SecretAccessKey != "file-secret-key" {
					t.Errorf("Expected SecretAccessKey from file, got different value")
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
			validateConfig: func(t *testing.T, cfg *aws.Config) {
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
			validateConfig: func(t *testing.T, cfg *aws.Config) {
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

			cfg := NewSession(ctx, tc.agent, tc.credentialsFile, tc.credKey, tc.credSecretKey, tc.region)

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

func TestNewSession_UserAgent(t *testing.T) {
	ctx := context.Background()
	agent := "test-user-agent"

	cfg := NewSession(ctx, agent, "", "test-key", "test-secret", "us-east-1")

	if cfg == nil {
		t.Fatal("Expected non-nil config")
	}
}

func TestNewSession_ContextPropagation(t *testing.T) {
	// Create a context with a specific value to verify it's being used
	type ctxKey string
	testKey := ctxKey("test")
	ctx := context.WithValue(context.Background(), testKey, "test-value")

	cfg := NewSession(ctx, "test-agent", "", "test-key", "test-secret", "us-east-1")

	if cfg == nil {
		t.Error("Expected non-nil config")
	}
}

func TestGetSession(t *testing.T) {
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
				for _, env := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
					t.Setenv(env, "")
				}
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
			name:        "When no credentials provided, it should fall back to SDK default chain",
			opts:        AWSCredentialsOptions{},
			agent:       "test-agent",
			region:      "us-east-1",
			expectError: false,
		},
		{
			name:        "When RoleArn set without credentials files, it should attempt role assumption via default chain and fail",
			opts:        AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			agent:       "test-agent",
			region:      "us-east-1",
			expectError: true,
		},
		{
			name:        "When STS credentials file does not exist, it should return error",
			opts:        AWSCredentialsOptions{STSCredentialsFile: "/nonexistent/creds.json", RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			agent:       "test-agent",
			region:      "us-east-1",
			expectError: true,
		},
		{
			name: "When secret data provided with RoleArn, it should attempt STS session and fail",
			opts: AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			setupFunc: func(t *testing.T) string {
				return "use-secret-data"
			},
			agent:       "test-agent",
			region:      "us-east-1",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var secretData *util.CredentialsSecretData
			if tc.setupFunc != nil {
				result := tc.setupFunc(t)
				if result == "use-secret-data" {
					secretData = &util.CredentialsSecretData{
						AWSAccessKeyID:     "test-access-key",
						AWSSecretAccessKey: "test-secret-key",
						AWSSessionToken:    "test-session-token",
					}
				} else {
					tc.opts.AWSCredentialsFile = result
				}
			}

			cfg, err := tc.opts.GetSession(ctx, tc.agent, secretData, tc.region)

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

func TestGetSession_DefaultChainRoleAssumption(t *testing.T) {
	original := assumeRoleFn
	t.Cleanup(func() { assumeRoleFn = original })

	testCases := []struct {
		name           string
		opts           AWSCredentialsOptions
		region         string
		validateConfig func(t *testing.T, cfg *aws.Config)
	}{
		{
			name:   "When RoleArn set with region, it should return config with assumed credentials and region",
			opts:   AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			region: "us-west-2",
			validateConfig: func(t *testing.T, cfg *aws.Config) {
				if cfg.Region != "us-west-2" {
					t.Errorf("expected region us-west-2, got %s", cfg.Region)
				}
				creds, err := cfg.Credentials.Retrieve(context.Background())
				if err != nil {
					t.Fatalf("failed to retrieve credentials: %v", err)
				}
				if creds.AccessKeyID != "assumed-key" {
					t.Errorf("expected AccessKeyID %q, got %q", "assumed-key", creds.AccessKeyID)
				}
			},
		},
		{
			name:   "When RoleArn set without region, it should return config with assumed credentials",
			opts:   AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			region: "",
			validateConfig: func(t *testing.T, cfg *aws.Config) {
				creds, err := cfg.Credentials.Retrieve(context.Background())
				if err != nil {
					t.Fatalf("failed to retrieve credentials: %v", err)
				}
				if creds.AccessKeyID != "assumed-key" {
					t.Errorf("expected AccessKeyID %q, got %q", "assumed-key", creds.AccessKeyID)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assumeRoleFn = func(_ context.Context, _ aws.Config, _, _ string) (*aws.Credentials, error) {
				return &aws.Credentials{
					AccessKeyID:     "assumed-key",
					SecretAccessKey: "assumed-secret",
					SessionToken:    "assumed-token",
				}, nil
			}

			cfg, err := tc.opts.GetSession(context.Background(), "test-agent", nil, tc.region)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
			tc.validateConfig(t, cfg)
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name        string
		opts        AWSCredentialsOptions
		expectError bool
		errorMsg    string
	}{
		{
			name:        "When no flags provided, it should succeed for SDK default chain",
			opts:        AWSCredentialsOptions{},
			expectError: false,
		},
		{
			name:        "When only role-arn provided, it should succeed for default chain with role assumption",
			opts:        AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			expectError: false,
		},
		{
			name:        "When sts-creds and role-arn provided, it should succeed",
			opts:        AWSCredentialsOptions{STSCredentialsFile: "/tmp/creds.json", RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			expectError: false,
		},
		{
			name:        "When only sts-creds provided without role-arn, it should fail",
			opts:        AWSCredentialsOptions{STSCredentialsFile: "/tmp/creds.json"},
			expectError: true,
			errorMsg:    "'role-arn' is required when 'sts-creds' is provided",
		},
		{
			name:        "When aws-creds combined with sts-creds, it should fail",
			opts:        AWSCredentialsOptions{AWSCredentialsFile: "/tmp/aws.ini", STSCredentialsFile: "/tmp/creds.json"},
			expectError: true,
		},
		{
			name:        "When aws-creds combined with role-arn, it should fail",
			opts:        AWSCredentialsOptions{AWSCredentialsFile: "/tmp/aws.ini", RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			expectError: true,
		},
		{
			name:        "When only aws-creds provided, it should succeed",
			opts:        AWSCredentialsOptions{AWSCredentialsFile: "/tmp/aws.ini"},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				} else if tc.errorMsg != "" && err.Error() != tc.errorMsg {
					t.Errorf("Expected error message %q, got %q", tc.errorMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestValidateProduct(t *testing.T) {
	testCases := []struct {
		name        string
		opts        AWSCredentialsOptions
		expectError bool
	}{
		{
			name:        "When no flags provided, it should fail",
			opts:        AWSCredentialsOptions{},
			expectError: true,
		},
		{
			name:        "When only role-arn provided, it should fail",
			opts:        AWSCredentialsOptions{RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			expectError: true,
		},
		{
			name:        "When only sts-creds provided, it should fail",
			opts:        AWSCredentialsOptions{STSCredentialsFile: "/tmp/creds.json"},
			expectError: true,
		},
		{
			name:        "When sts-creds and role-arn provided, it should succeed",
			opts:        AWSCredentialsOptions{STSCredentialsFile: "/tmp/creds.json", RoleArn: "arn:aws:iam::123456789012:role/test-role"},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.ValidateProduct()
			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}
