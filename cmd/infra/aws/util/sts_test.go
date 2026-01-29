package util

import (
	"context"
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
			name:            "nil credentials",
			agent:           "test-agent",
			roleArn:         "arn:aws:iam::123456789012:role/TestRole",
			region:          "us-east-1",
			assumeRoleCreds: nil,
			expectError:     true,
			errorContains:   "assumeRoleCreds cannot be nil",
		},
		{
			name:    "empty roleArn",
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
			name:    "empty agent",
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
			name:    "valid parameters but will fail on actual assume role call",
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
