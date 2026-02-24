package metricsproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization string
		expected      string
	}{
		{
			name:          "When Authorization header contains a valid Bearer token, it should return the token",
			authorization: "Bearer my-valid-token-12345",
			expected:      "my-valid-token-12345",
		},
		{
			name:          "When Authorization header is missing, it should return empty string",
			authorization: "",
			expected:      "",
		},
		{
			name:          "When Authorization header uses Basic auth, it should return empty string",
			authorization: "Basic dXNlcjpwYXNzd29yZA==",
			expected:      "",
		},
		{
			name:          "When Authorization header uses lowercase bearer, it should return the token",
			authorization: "bearer my-token-lowercase",
			expected:      "my-token-lowercase",
		},
		{
			name:          "When Authorization header uses uppercase BEARER, it should return the token",
			authorization: "BEARER my-token-uppercase",
			expected:      "my-token-uppercase",
		},
		{
			name:          "When Authorization header uses mixed case BeArEr, it should return the token",
			authorization: "BeArEr my-token-mixedcase",
			expected:      "my-token-mixedcase",
		},
		{
			name:          "When Authorization header has Bearer with empty token, it should return empty string",
			authorization: "Bearer ",
			expected:      "",
		},
		{
			name:          "When Authorization header has only Bearer without space, it should return empty string",
			authorization: "Bearer",
			expected:      "",
		},
		{
			name:          "When Authorization header has token with spaces, it should return the full token including spaces",
			authorization: "Bearer token with spaces",
			expected:      "token with spaces",
		},
		{
			name:          "When Authorization header uses different auth scheme, it should return empty string",
			authorization: "Digest username=\"user\"",
			expected:      "",
		},
		{
			name:          "When Authorization header has multiple spaces after Bearer, it should return the spaces",
			authorization: "Bearer   ",
			expected:      "  ",
		},
		{
			name:          "When Authorization header contains Bearer token with special characters, it should return the token",
			authorization: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
			expected:      "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/metrics/test", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}

			result := extractBearerToken(req)

			if result != tt.expected {
				t.Errorf("extractBearerToken() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
