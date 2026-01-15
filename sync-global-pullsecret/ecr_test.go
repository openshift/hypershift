package syncglobalpullsecret

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/smithy-go"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// TestParseECRToken tests the ECR token parsing functionality
func TestParseECRToken(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		wantUsername string
		wantPassword string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "When given valid ECR token it should parse successfully",
			token:        base64.StdEncoding.EncodeToString([]byte("AWS:my-password-123")),
			wantUsername: "AWS",
			wantPassword: "my-password-123",
			wantErr:      false,
		},
		{
			name:        "When given invalid base64 it should return error",
			token:       "not-valid-base64!@#",
			wantErr:     true,
			errContains: "failed to decode ECR token",
		},
		{
			name:        "When given token without AWS prefix it should return error",
			token:       base64.StdEncoding.EncodeToString([]byte("NOTAWS:password")),
			wantErr:     true,
			errContains: "invalid ECR token format",
		},
		{
			name:        "When given token without colon separator it should return error",
			token:       base64.StdEncoding.EncodeToString([]byte("AWSpassword")),
			wantErr:     true,
			errContains: "invalid ECR token format",
		},
		{
			name:         "When given token with password containing colons it should parse correctly",
			token:        base64.StdEncoding.EncodeToString([]byte("AWS:pass:word:123")),
			wantUsername: "AWS",
			wantPassword: "pass:word:123",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, password, err := parseECRToken(tt.token)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseECRToken() expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseECRToken() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("parseECRToken() unexpected error = %v", err)
				return
			}
			if username != tt.wantUsername {
				t.Errorf("parseECRToken() username = %v, want %v", username, tt.wantUsername)
			}
			if password != tt.wantPassword {
				t.Errorf("parseECRToken() password = %v, want %v", password, tt.wantPassword)
			}
		})
	}
}

// TestValidateECRRegistry tests ECR registry URL validation
func TestValidateECRRegistry(t *testing.T) {
	tests := []struct {
		name        string
		registry    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "When given valid ECR registry URL it should validate successfully",
			registry: "123456789012.dkr.ecr.us-east-1.amazonaws.com",
			wantErr:  false,
		},
		{
			name:     "When given valid ECR registry URL with amazonaws suffix it should validate successfully",
			registry: "123456789012.dkr.ecr.us-west-2.amazonaws.com",
			wantErr:  false,
		},
		{
			name:        "When given URL with wrong number of parts it should return error",
			registry:    "123456789012.dkr.ecr.us-east-1",
			wantErr:     true,
			errContains: "expected format",
		},
		{
			name:        "When given URL without dkr component it should return error",
			registry:    "123456789012.xxx.ecr.us-east-1.amazonaws.com",
			wantErr:     true,
			errContains: "not a valid ECR registry URL",
		},
		{
			name:        "When given URL without ecr component it should return error",
			registry:    "123456789012.dkr.xxx.us-east-1.amazonaws.com",
			wantErr:     true,
			errContains: "not a valid ECR registry URL",
		},
		{
			name:        "When given non-AWS URL it should return error",
			registry:    "123456789012.dkr.ecr.us-east-1.example.com",
			wantErr:     true,
			errContains: "not an AWS ECR registry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateECRRegistry(tt.registry)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateECRRegistry() expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateECRRegistry() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("validateECRRegistry() unexpected error = %v", err)
			}
		})
	}
}

// TestCachedCredentialIsValid tests credential cache expiration logic
func TestCachedCredentialIsValid(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "When credential expires in 2 hours it should be valid",
			expiresAt: time.Now().Add(2 * time.Hour),
			want:      true,
		},
		{
			name:      "When credential expires in 1 hour and 1 minute it should be valid",
			expiresAt: time.Now().Add(1*time.Hour + 1*time.Minute),
			want:      true,
		},
		{
			name:      "When credential expires in exactly 1 hour it should be invalid",
			expiresAt: time.Now().Add(1 * time.Hour),
			want:      false,
		},
		{
			name:      "When credential expires in 30 minutes it should be invalid",
			expiresAt: time.Now().Add(30 * time.Minute),
			want:      false,
		},
		{
			name:      "When credential is already expired it should be invalid",
			expiresAt: time.Now().Add(-1 * time.Hour),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred := &cachedCredential{
				authToken: "test-token",
				expiresAt: tt.expiresAt,
			}
			if got := cred.isValid(); got != tt.want {
				t.Errorf("isValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFetchECRCredentials tests fetching ECR credentials
func TestFetchECRCredentials(t *testing.T) {
	tests := []struct {
		name           string
		registries     []string
		mockResponse   *ecr.GetAuthorizationTokenOutput
		mockError      error
		wantErr        bool
		wantCredsCount int
		errContains    string
	}{
		{
			name:       "When ECR API returns valid credentials it should cache them",
			registries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			mockResponse: &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{
						AuthorizationToken: stringPtr(base64.StdEncoding.EncodeToString([]byte("AWS:password123"))),
						ExpiresAt:          timePtr(time.Now().Add(12 * time.Hour)),
					},
				},
			},
			wantErr:        false,
			wantCredsCount: 1,
		},
		{
			name:       "When multiple registries configured it should apply same token to all",
			registries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com", "123456789012.dkr.ecr.us-west-2.amazonaws.com"},
			mockResponse: &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{
						AuthorizationToken: stringPtr(base64.StdEncoding.EncodeToString([]byte("AWS:password123"))),
						ExpiresAt:          timePtr(time.Now().Add(12 * time.Hour)),
					},
				},
			},
			wantErr:        false,
			wantCredsCount: 2,
		},
		{
			name:           "When no registries are configured it should return nil",
			registries:     []string{},
			wantErr:        false,
			wantCredsCount: 0,
		},
		{
			name:        "When ECR API returns error it should fail",
			registries:  []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			mockError:   errors.New("API error"),
			wantErr:     true,
			errContains: "failed to get ECR authorization token",
		},
		{
			name:       "When ECR API is throttled it should return cached credentials",
			registries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			mockError:  &mockAPIError{code: "ThrottlingException"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := NewMockecrClient(ctrl)
			if tt.mockResponse != nil || tt.mockError != nil {
				mockClient.EXPECT().
					GetAuthorizationToken(gomock.Any(), gomock.Any()).
					Return(tt.mockResponse, tt.mockError).
					AnyTimes()
			}

			syncer := &GlobalPullSecretSyncer{
				ecrClient:     mockClient,
				ecrRegistries: tt.registries,
				ecrCredCache: &ecrCredentialCache{
					credentials: make(map[string]*cachedCredential),
				},
				log: testLogger(t),
			}

			creds, err := syncer.fetchECRCredentials(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Errorf("fetchECRCredentials() expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("fetchECRCredentials() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("fetchECRCredentials() unexpected error = %v", err)
				return
			}

			if len(creds) != tt.wantCredsCount {
				t.Errorf("fetchECRCredentials() got %d credentials, want %d", len(creds), tt.wantCredsCount)
			}

			// Verify cache was updated
			syncer.ecrCredCache.mu.RLock()
			if len(syncer.ecrCredCache.credentials) != tt.wantCredsCount {
				t.Errorf("cache has %d credentials, want %d", len(syncer.ecrCredCache.credentials), tt.wantCredsCount)
			}
			syncer.ecrCredCache.mu.RUnlock()
		})
	}
}

// TestBuildDockerConfigWithECR tests merging ECR credentials with existing pull secrets
func TestBuildDockerConfigWithECR(t *testing.T) {
	validToken := base64.StdEncoding.EncodeToString([]byte("AWS:password123"))

	tests := []struct {
		name             string
		basePullSecret   map[string]any
		cachedCreds      map[string]*cachedCredential
		wantRegistries   []string
		wantErr          bool
		errContains      string
		wantOriginalAuth bool
	}{
		{
			name: "When base pull secret is empty it should add ECR credentials",
			basePullSecret: map[string]any{
				"auths": map[string]any{},
			},
			cachedCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(12 * time.Hour),
				},
			},
			wantRegistries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			wantErr:        false,
		},
		{
			name: "When registry already exists in base it should preserve original",
			basePullSecret: map[string]any{
				"auths": map[string]any{
					"123456789012.dkr.ecr.us-east-1.amazonaws.com": map[string]any{
						"auth": "original-auth",
					},
				},
			},
			cachedCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(12 * time.Hour),
				},
			},
			wantRegistries:   []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			wantOriginalAuth: true,
			wantErr:          false,
		},
		{
			name: "When credential is expired it should skip it",
			basePullSecret: map[string]any{
				"auths": map[string]any{},
			},
			cachedCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(30 * time.Minute), // Expires within 1 hour buffer
				},
			},
			wantRegistries: []string{},
			wantErr:        false,
		},
		{
			name:           "When cache is empty it should return base pull secret unchanged",
			basePullSecret: map[string]any{"auths": map[string]any{}},
			cachedCreds:    map[string]*cachedCredential{},
			wantRegistries: []string{},
			wantErr:        false,
		},
		{
			name:           "When base pull secret is invalid JSON it should return error",
			basePullSecret: nil,
			cachedCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(12 * time.Hour),
				},
			},
			wantErr:     true,
			errContains: "failed to unmarshal base pull secret",
		},
		{
			name: "When base pull secret missing auths key it should return error",
			basePullSecret: map[string]any{
				"wrongkey": map[string]any{},
			},
			cachedCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(12 * time.Hour),
				},
			},
			wantErr:     true,
			errContains: "missing 'auths' key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var basePullSecretBytes []byte
			var err error
			if tt.basePullSecret != nil {
				basePullSecretBytes, err = json.Marshal(tt.basePullSecret)
				if err != nil {
					t.Fatalf("failed to marshal test base pull secret: %v", err)
				}
			} else {
				basePullSecretBytes = []byte("invalid json")
			}

			syncer := &GlobalPullSecretSyncer{
				ecrCredCache: &ecrCredentialCache{
					credentials: tt.cachedCreds,
				},
				log: testLogger(t),
			}

			result, err := syncer.buildDockerConfigWithECR(basePullSecretBytes)
			if tt.wantErr {
				if err == nil {
					t.Errorf("buildDockerConfigWithECR() expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("buildDockerConfigWithECR() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("buildDockerConfigWithECR() unexpected error = %v", err)
				return
			}

			// Parse result
			var resultConfig map[string]any
			if err := json.Unmarshal(result, &resultConfig); err != nil {
				t.Fatalf("failed to unmarshal result: %v", err)
			}

			auths, ok := resultConfig["auths"].(map[string]any)
			if !ok {
				t.Fatalf("result missing auths key")
			}

			// Verify expected registries present
			for _, registry := range tt.wantRegistries {
				if _, exists := auths[registry]; !exists {
					t.Errorf("expected registry %s not found in result", registry)
				}
			}

			// Verify original auth preserved if expected
			if tt.wantOriginalAuth {
				for _, registry := range tt.wantRegistries {
					authEntry, ok := auths[registry].(map[string]any)
					if !ok {
						continue
					}
					if authEntry["auth"] == "original-auth" {
						return // Found original auth, test passes
					}
				}
				t.Errorf("expected original auth to be preserved but it wasn't")
			}
		})
	}
}

// TestRefreshECRCredentialsIfNeeded tests on-demand credential refresh
func TestRefreshECRCredentialsIfNeeded(t *testing.T) {
	validToken := base64.StdEncoding.EncodeToString([]byte("AWS:password123"))

	tests := []struct {
		name          string
		registries    []string
		existingCreds map[string]*cachedCredential
		mockResponse  *ecr.GetAuthorizationTokenOutput
		mockError     error
		expectAPICall bool
		wantErr       bool
	}{
		{
			name:       "When credentials are valid it should not refresh",
			registries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			existingCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(2 * time.Hour),
				},
			},
			expectAPICall: false,
			wantErr:       false,
		},
		{
			name:       "When credentials expire soon it should refresh",
			registries: []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			existingCreds: map[string]*cachedCredential{
				"123456789012.dkr.ecr.us-east-1.amazonaws.com": {
					authToken: validToken,
					expiresAt: time.Now().Add(30 * time.Minute),
				},
			},
			mockResponse: &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{
						AuthorizationToken: stringPtr(validToken),
						ExpiresAt:          timePtr(time.Now().Add(12 * time.Hour)),
					},
				},
			},
			expectAPICall: true,
			wantErr:       false,
		},
		{
			name:          "When cache is empty it should refresh",
			registries:    []string{"123456789012.dkr.ecr.us-east-1.amazonaws.com"},
			existingCreds: map[string]*cachedCredential{},
			mockResponse: &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{
						AuthorizationToken: stringPtr(validToken),
						ExpiresAt:          timePtr(time.Now().Add(12 * time.Hour)),
					},
				},
			},
			expectAPICall: true,
			wantErr:       false,
		},
		{
			name:          "When no registries configured it should not refresh",
			registries:    []string{},
			existingCreds: map[string]*cachedCredential{},
			expectAPICall: false,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := NewMockecrClient(ctrl)
			if tt.expectAPICall {
				mockClient.EXPECT().
					GetAuthorizationToken(gomock.Any(), gomock.Any()).
					Return(tt.mockResponse, tt.mockError).
					Times(1)
			}

			syncer := &GlobalPullSecretSyncer{
				ecrClient:     mockClient,
				ecrRegistries: tt.registries,
				ecrCredCache: &ecrCredentialCache{
					credentials: tt.existingCreds,
				},
				log: testLogger(t),
			}

			err := syncer.refreshECRCredentialsIfNeeded(context.Background())
			if tt.wantErr && err == nil {
				t.Errorf("refreshECRCredentialsIfNeeded() expected error but got none")
				return
			}
			if !tt.wantErr && err != nil {
				t.Errorf("refreshECRCredentialsIfNeeded() unexpected error = %v", err)
				return
			}
		})
	}
}

// TestGetCachedCredentials tests retrieving cached credentials
func TestGetCachedCredentials(t *testing.T) {
	validToken := base64.StdEncoding.EncodeToString([]byte("AWS:password123"))

	t.Run("When cache has credentials it should return them all", func(t *testing.T) {
		syncer := &GlobalPullSecretSyncer{
			ecrCredCache: &ecrCredentialCache{
				credentials: map[string]*cachedCredential{
					"registry1": {authToken: validToken, expiresAt: time.Now().Add(1 * time.Hour)},
					"registry2": {authToken: validToken, expiresAt: time.Now().Add(2 * time.Hour)},
				},
			},
		}

		creds := syncer.getCachedCredentials()
		if len(creds) != 2 {
			t.Errorf("getCachedCredentials() returned %d credentials, want 2", len(creds))
		}
		if creds["registry1"] != validToken {
			t.Errorf("getCachedCredentials() registry1 token mismatch")
		}
		if creds["registry2"] != validToken {
			t.Errorf("getCachedCredentials() registry2 token mismatch")
		}
	})

	t.Run("When cache is empty it should return empty map", func(t *testing.T) {
		syncer := &GlobalPullSecretSyncer{
			ecrCredCache: &ecrCredentialCache{
				credentials: map[string]*cachedCredential{},
			},
		}

		creds := syncer.getCachedCredentials()
		if len(creds) != 0 {
			t.Errorf("getCachedCredentials() returned %d credentials, want 0", len(creds))
		}
	})
}

// Helper types and functions

type mockAPIError struct {
	code string
}

func (e *mockAPIError) Error() string {
	return e.code
}

func (e *mockAPIError) ErrorCode() string {
	return e.code
}

func (e *mockAPIError) ErrorMessage() string {
	return e.code
}

func (e *mockAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultUnknown
}

func stringPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func testLogger(t *testing.T) logr.Logger {
	zapLogger := zaptest.NewLogger(t, zaptest.Level(zap.ErrorLevel))
	return zapr.NewLogger(zapLogger)
}
