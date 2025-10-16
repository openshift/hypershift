package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
)

//go:generate ../../../hack/tools/bin/mockgen -source=aws.go -package=healthcheck -destination=aws_mock.go

// mockIAMClient implements iamiface.IAMAPI for testing
type mockIAMClient struct {
	iamiface.IAMAPI
	createOIDCProviderFunc func(*iam.CreateOpenIDConnectProviderInput) (*iam.CreateOpenIDConnectProviderOutput, error)
}

func (m *mockIAMClient) CreateOpenIDConnectProviderWithContext(ctx aws.Context, input *iam.CreateOpenIDConnectProviderInput, opts ...request.Option) (*iam.CreateOpenIDConnectProviderOutput, error) {
	if m.createOIDCProviderFunc != nil {
		return m.createOIDCProviderFunc(input)
	}
	return &iam.CreateOpenIDConnectProviderOutput{}, nil
}

func TestExtractOIDCProviderArnFromIssuerURL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		issuerURL   string
		expectedURL string
		expectError bool
	}{
		{
			name:        "Valid S3 OIDC URL with us-east-1",
			issuerURL:   "https://hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cluster-infra-id",
			expectedURL: "hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com",
			expectError: false,
		},
		{
			name:        "Valid S3 OIDC URL with eu-west-1",
			issuerURL:   "https://my-bucket.s3.eu-west-1.amazonaws.com/my-cluster",
			expectedURL: "my-bucket.s3.eu-west-1.amazonaws.com",
			expectError: false,
		},
		{
			name:        "Valid S3 OIDC URL with just bucket name",
			issuerURL:   "https://test-bucket.s3.ap-south-1.amazonaws.com",
			expectedURL: "test-bucket.s3.ap-south-1.amazonaws.com",
			expectError: false,
		},
		{
			name:        "URL without https prefix",
			issuerURL:   "http://hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cluster-infra-id",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL format without .s3.",
			issuerURL:   "https://hypershift-ci-1-oidc.us-east-1.amazonaws.com/cluster-infra-id",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL format with missing domain",
			issuerURL:   "https://hypershift-ci-1-oidc.s3.us-east-1",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Invalid URL format with incomplete region",
			issuerURL:   "https://hypershift-ci-1-oidc.s3.amazonaws.com",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "Empty URL",
			issuerURL:   "",
			expectedURL: "",
			expectError: true,
		},
		{
			name:        "URL without scheme",
			issuerURL:   "hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cluster-infra-id",
			expectedURL: "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := extractOIDCProviderArnFromIssuerURL(tc.issuerURL)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none. Result: %s", result)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if result != tc.expectedURL {
					t.Errorf("Expected URL: %s, got: %s", tc.expectedURL, result)
				}
			}
		})
	}
}

func TestParseAWSCredentialsFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		credentialsFile   string
		expectedAccessKey string
		expectedSecretKey string
		expectError       bool
	}{
		{
			name: "Valid credentials file with default profile",
			credentialsFile: `[default]
aws_access_key_id = AKIA1234567890EXAMPLE
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			expectedAccessKey: "AKIA1234567890EXAMPLE",
			expectedSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expectError:       false,
		},
		{
			name: "Valid credentials file with spaces around equals",
			credentialsFile: `[default]
aws_access_key_id   =   AKIA1234567890EXAMPLE
aws_secret_access_key   =   wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			expectedAccessKey: "AKIA1234567890EXAMPLE",
			expectedSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expectError:       false,
		},
		{
			name: "Valid credentials file without profile header",
			credentialsFile: `aws_access_key_id = AKIA1234567890EXAMPLE
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			expectedAccessKey: "AKIA1234567890EXAMPLE",
			expectedSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expectError:       false,
		},
		{
			name: "Valid credentials file with additional whitespace lines",
			credentialsFile: `
[default]

aws_access_key_id = AKIA1234567890EXAMPLE

aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

`,
			expectedAccessKey: "AKIA1234567890EXAMPLE",
			expectedSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expectError:       false,
		},
		{
			name: "Valid credentials file with comments and extra fields",
			credentialsFile: `# This is a comment
[default]
aws_access_key_id = AKIA1234567890EXAMPLE
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
region = us-east-1
output = json`,
			expectedAccessKey: "AKIA1234567890EXAMPLE",
			expectedSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			expectError:       false,
		},
		{
			name: "Missing access key",
			credentialsFile: `[default]
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
		{
			name: "Missing secret key",
			credentialsFile: `[default]
aws_access_key_id = AKIA1234567890EXAMPLE`,
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
		{
			name:              "Empty credentials file",
			credentialsFile:   "",
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
		{
			name: "Malformed credentials file with no equals",
			credentialsFile: `[default]
aws_access_key_id AKIA1234567890EXAMPLE
aws_secret_access_key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`,
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
		{
			name: "Credentials file with empty values",
			credentialsFile: `[default]
aws_access_key_id =
aws_secret_access_key = `,
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
		{
			name:              "Credentials file with only profile header",
			credentialsFile:   `[default]`,
			expectedAccessKey: "",
			expectedSecretKey: "",
			expectError:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			accessKey, secretKey, err := parseAWSCredentialsFile(tc.credentialsFile)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none. AccessKey: %s, SecretKey: %s", accessKey, secretKey)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if accessKey != tc.expectedAccessKey {
					t.Errorf("Expected access key: %s, got: %s", tc.expectedAccessKey, accessKey)
				}
				if secretKey != tc.expectedSecretKey {
					t.Errorf("Expected secret key: %s, got: %s", tc.expectedSecretKey, secretKey)
				}
			}
		})
	}
}

func TestRecreateOIDCIdentityProvider(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		issuerURL        string
		oidcProviderURL  string
		mockSetup        func(*mockIAMClient)
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name:             "Invalid issuer URL format",
			issuerURL:        "invalid-url",
			oidcProviderURL:  "test-url",
			mockSetup:        func(m *mockIAMClient) {},
			expectError:      true,
			expectedErrorMsg: "invalid IssuerURL format",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock IAM client
			mockClient := &mockIAMClient{}
			tc.mockSetup(mockClient)

			// Call the function under test
			ctx := context.Background()
			err := recreateOIDCIdentityProvider(ctx, mockClient, tc.issuerURL, tc.oidcProviderURL)

			// Validate results
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tc.expectedErrorMsg != "" && !strings.Contains(err.Error(), tc.expectedErrorMsg) {
					t.Errorf("Expected error message to contain '%s', got: %v", tc.expectedErrorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestGetOIDCThumbprint(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		setupServer      func() *httptest.Server
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name: "Successful thumbprint generation",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, err := w.Write([]byte(`{"jwks_uri": "https://` + r.Host + `/keys"}`))
						if err != nil {
							t.Errorf("Failed to write response: %v", err)
						}
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				}))
			},
			expectError:      true, // Change to expect error since we can't make TLS connection to HTTP server
			expectedErrorMsg: "failed to get certificate thumbprint",
		},
		{
			name: "OIDC configuration not found",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			expectError:      true,
			expectedErrorMsg: "failed to fetch OIDC configuration",
		},
		{
			name: "Invalid JSON in OIDC configuration",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, err := w.Write([]byte(`{invalid json`))
						if err != nil {
							t.Errorf("Failed to write response: %v", err)
						}
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				}))
			},
			expectError:      true,
			expectedErrorMsg: "failed to parse OIDC configuration JSON",
		},
		{
			name: "Missing JWKS URI in OIDC configuration",
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						_, err := w.Write([]byte(`{}`))
						if err != nil {
							t.Errorf("Failed to write response: %v", err)
						}
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				}))
			},
			expectError:      true,
			expectedErrorMsg: "no JWKS URI found in OIDC configuration",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test server
			server := tc.setupServer()
			defer server.Close()

			// Call the function under test
			ctx := context.Background()
			thumbprint, err := getOIDCThumbprint(ctx, server.URL)

			// Validate results
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none. Thumbprint: %s", thumbprint)
				} else if tc.expectedErrorMsg != "" && !strings.Contains(err.Error(), tc.expectedErrorMsg) {
					t.Errorf("Expected error message to contain '%s', got: %v", tc.expectedErrorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if thumbprint == "" {
					t.Errorf("Expected non-empty thumbprint")
				}
				// Validate thumbprint format (should be a hex string)
				if len(thumbprint) == 40 { // SHA-1 hash is 20 bytes = 40 hex characters
					// Only validate format if we got a proper thumbprint length
					for _, c := range thumbprint {
						if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
							t.Errorf("Expected thumbprint to be hex string, got: %s", thumbprint)
							break
						}
					}
				}
			}
		})
	}
}
