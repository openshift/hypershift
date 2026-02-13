package util

import (
	"context"
	"os"
	"strings"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewSTSSessionV2_Validation(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name            string
		agent           string
		roleArn         string
		region          string
		assumeRoleCreds *awsv2.Credentials
		expectError     bool
		errorContains   string
	}{
		{
			name:            "When credentials are nil, it should return error",
			agent:           "test-agent",
			roleArn:         "arn:aws:iam::123456789012:role/TestRole",
			region:          "us-east-1",
			assumeRoleCreds: nil,
			expectError:     true,
			errorContains:   "assumeRoleCreds cannot be nil",
		},
		{
			name:    "When roleArn is empty, it should return error",
			agent:   "test-agent",
			roleArn: "",
			region:  "us-east-1",
			assumeRoleCreds: &awsv2.Credentials{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			expectError:   true,
			errorContains: "roleArn cannot be empty",
		},
		{
			name:    "When agent is empty, it should return error",
			agent:   "",
			roleArn: "arn:aws:iam::123456789012:role/TestRole",
			region:  "us-east-1",
			assumeRoleCreds: &awsv2.Credentials{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
			},
			expectError:   true,
			errorContains: "agent cannot be empty",
		},
		{
			name:    "When given valid parameters, it should attempt AssumeRole call",
			agent:   "test-agent",
			roleArn: "arn:aws:iam::123456789012:role/TestRole",
			region:  "us-east-1",
			assumeRoleCreds: &awsv2.Credentials{
				AccessKeyID:     "test-key",
				SecretAccessKey: "test-secret",
				SessionToken:    "test-token",
			},
			expectError:   true,
			errorContains: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewSTSSessionV2(ctx, tc.agent, tc.roleArn, tc.region, tc.assumeRoleCreds)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
					return
				}
				if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q, got %q", tc.errorContains, err.Error())
				}
				if cfg != nil {
					t.Error("Expected nil config on error")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if cfg == nil {
					t.Error("Expected non-nil config")
				}
			}
		})
	}
}

func TestNewSTSSessionV2_ValidationOrder(t *testing.T) {
	ctx := context.Background()

	// Test that validation happens in the correct order
	// and returns the first validation error

	// Should return assumeRoleCreds error first
	_, err := NewSTSSessionV2(ctx, "", "", "us-east-1", nil)
	if err == nil {
		t.Fatal("Expected error but got nil")
	}
	if !strings.Contains(err.Error(), "assumeRoleCreds cannot be nil") {
		t.Errorf("Expected assumeRoleCreds validation error first, got: %v", err)
	}

	// Should return roleArn error when credentials are valid
	_, err = NewSTSSessionV2(ctx, "", "", "us-east-1", &awsv2.Credentials{
		AccessKeyID:     "test",
		SecretAccessKey: "test",
	})
	if err == nil {
		t.Fatal("Expected error but got nil")
	}
	if !strings.Contains(err.Error(), "roleArn cannot be empty") {
		t.Errorf("Expected roleArn validation error, got: %v", err)
	}

	// Should return agent error when credentials and roleArn are valid
	_, err = NewSTSSessionV2(ctx, "", "arn:aws:iam::123456789012:role/TestRole", "us-east-1", &awsv2.Credentials{
		AccessKeyID:     "test",
		SecretAccessKey: "test",
	})
	if err == nil {
		t.Fatal("Expected error but got nil")
	}
	if !strings.Contains(err.Error(), "agent cannot be empty") {
		t.Errorf("Expected agent validation error, got: %v", err)
	}
}

func TestParseSTSCredentialsFileV2(t *testing.T) {
	testCases := []struct {
		name          string
		fileContent   string
		setupFunc     func(t *testing.T) string
		expectError   bool
		errorContains string
		validateCreds func(t *testing.T, creds *awsv2.Credentials)
	}{
		{
			name: "When given valid STS credentials file, it should parse successfully",
			fileContent: `{
  "Credentials": {
    "AccessKeyId": "ASIA1234567890EXAMPLE",
    "SecretAccessKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "SessionToken": "FwoGZXIvYXdzEBYaDH...",
    "Expiration": "2025-01-30T10:00:00Z"
  }
}`,
			expectError: false,
			validateCreds: func(t *testing.T, creds *awsv2.Credentials) {
				if creds.AccessKeyID != "ASIA1234567890EXAMPLE" {
					t.Errorf("Expected AccessKeyID ASIA1234567890EXAMPLE, got %s", creds.AccessKeyID)
				}
				if creds.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
					t.Errorf("Expected correct SecretAccessKey, got %s", creds.SecretAccessKey)
				}
				if creds.SessionToken != "FwoGZXIvYXdzEBYaDH..." {
					t.Errorf("Expected correct SessionToken, got %s", creds.SessionToken)
				}
			},
		},
		{
			name: "When credentials have empty expiration, it should parse successfully",
			fileContent: `{
  "Credentials": {
    "AccessKeyId": "TESTKEY123",
    "SecretAccessKey": "TESTSECRET456",
    "SessionToken": "TESTTOKEN789",
    "Expiration": ""
  }
}`,
			expectError: false,
			validateCreds: func(t *testing.T, creds *awsv2.Credentials) {
				if creds.AccessKeyID != "TESTKEY123" {
					t.Errorf("Expected AccessKeyID TESTKEY123, got %s", creds.AccessKeyID)
				}
			},
		},
		{
			name: "When file does not exist, it should return error",
			setupFunc: func(t *testing.T) string {
				return "/nonexistent/path/to/credentials.json"
			},
			expectError:   true,
			errorContains: "failed to read sts credentials file",
		},
		{
			name:          "When JSON format is invalid, it should return error",
			fileContent:   `{"Credentials": invalid json}`,
			expectError:   true,
			errorContains: "failed to unmarshal sts credentials",
		},
		{
			name:          "When file is empty, it should return error",
			fileContent:   ``,
			expectError:   true,
			errorContains: "failed to unmarshal sts credentials",
		},
		{
			name: "When Credentials field is missing, it should return empty credentials",
			fileContent: `{
  "SomeOtherField": "value"
}`,
			expectError: false,
			validateCreds: func(t *testing.T, creds *awsv2.Credentials) {
				// When Credentials field is missing, all fields will be empty strings
				if creds.AccessKeyID != "" {
					t.Errorf("Expected empty AccessKeyID, got %s", creds.AccessKeyID)
				}
				if creds.SecretAccessKey != "" {
					t.Errorf("Expected empty SecretAccessKey, got %s", creds.SecretAccessKey)
				}
			},
		},
		{
			name: "When credentials are partial, it should parse available fields",
			fileContent: `{
  "Credentials": {
    "AccessKeyId": "PARTIALKEY",
    "SecretAccessKey": ""
  }
}`,
			expectError: false,
			validateCreds: func(t *testing.T, creds *awsv2.Credentials) {
				if creds.AccessKeyID != "PARTIALKEY" {
					t.Errorf("Expected AccessKeyID PARTIALKEY, got %s", creds.AccessKeyID)
				}
				if creds.SecretAccessKey != "" {
					t.Errorf("Expected empty SecretAccessKey, got %s", creds.SecretAccessKey)
				}
				if creds.SessionToken != "" {
					t.Errorf("Expected empty SessionToken, got %s", creds.SessionToken)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var filePath string

			if tc.setupFunc != nil {
				filePath = tc.setupFunc(t)
			} else {
				// Create temporary file with test content
				tmpFile, err := os.CreateTemp(t.TempDir(), "sts-creds-*.json")
				if err != nil {
					t.Fatalf("Failed to create temp file: %v", err)
				}
				filePath = tmpFile.Name()

				if tc.fileContent != "" {
					if _, err := tmpFile.WriteString(tc.fileContent); err != nil {
						t.Fatalf("Failed to write to temp file: %v", err)
					}
				}
				tmpFile.Close()
			}

			creds, err := ParseSTSCredentialsFileV2(filePath)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
					return
				}
				if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q, got %q", tc.errorContains, err.Error())
				}
				if creds != nil {
					t.Error("Expected nil credentials on error")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
					return
				}
				if creds == nil {
					t.Error("Expected non-nil credentials")
					return
				}
				if tc.validateCreds != nil {
					tc.validateCreds(t, creds)
				}
			}
		})
	}
}

func TestParseSTSCredentialsFileV2_RealWorldFormat(t *testing.T) {
	realWorldJSON := `{
  "Credentials": {
    "AccessKeyId": "ASIAIOSFODNN7EXAMPLE",
    "SecretAccessKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYzEXAMPLEKEY",
    "SessionToken": "FwoGZXIvYXdzEBYaDHExampleToken1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=",
    "Expiration": "2025-01-30T12:00:00Z"
  }
}`

	tmpFile, err := os.CreateTemp(t.TempDir(), "real-world-sts-*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(realWorldJSON); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	creds, err := ParseSTSCredentialsFileV2(tmpFile.Name())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
		return
	}

	if creds == nil {
		t.Fatal("Expected non-nil credentials")
		return
	}

	expectedAccessKey := "ASIAIOSFODNN7EXAMPLE"
	if creds.AccessKeyID != expectedAccessKey {
		t.Errorf("Expected AccessKeyID %s, got %s", expectedAccessKey, creds.AccessKeyID)
	}

	if creds.SecretAccessKey == "" {
		t.Error("Expected non-empty SecretAccessKey")
	}

	if creds.SessionToken == "" {
		t.Error("Expected non-empty SessionToken")
	}
}
