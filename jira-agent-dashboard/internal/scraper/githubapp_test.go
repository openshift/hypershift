package scraper

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func generateTestKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

func TestNewGitHubAppAuth_ValidPKCS1Key(t *testing.T) {
	_, pemBytes := generateTestKey(t)
	auth, err := NewGitHubAppAuth("123", "456", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.appID != "123" {
		t.Errorf("expected appID %q, got %q", "123", auth.appID)
	}
	if auth.installationID != "456" {
		t.Errorf("expected installationID %q, got %q", "456", auth.installationID)
	}
}

func TestNewGitHubAppAuth_ValidPKCS8Key(t *testing.T) {
	key, _ := generateTestKey(t)
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling PKCS8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})

	auth, err := NewGitHubAppAuth("123", "456", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.privateKey == nil {
		t.Fatal("expected non-nil private key")
	}
}

func TestNewGitHubAppAuth_InvalidPEM(t *testing.T) {
	_, err := NewGitHubAppAuth("123", "456", []byte("not a pem"))
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestNewGitHubAppAuth_InvalidKeyBytes(t *testing.T) {
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: []byte("garbage"),
	})
	_, err := NewGitHubAppAuth("123", "456", pemBytes)
	if err == nil {
		t.Fatal("expected error for invalid key bytes")
	}
}

func TestGitHubAppAuth_CreateJWT(t *testing.T) {
	_, pemBytes := generateTestKey(t)
	auth, err := NewGitHubAppAuth("42", "99", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	jwt, err := auth.createJWT()
	if err != nil {
		t.Fatalf("creating JWT: %v", err)
	}

	// JWT should have 3 dot-separated parts
	parts := splitJWT(jwt)
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
}

func splitJWT(token string) []string {
	var parts []string
	start := 0
	for i, c := range token {
		if c == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	parts = append(parts, token[start:])
	return parts
}

func TestGitHubAppAuth_Token_CachesToken(t *testing.T) {
	_, pemBytes := generateTestKey(t)
	auth, err := NewGitHubAppAuth("42", "99", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      fmt.Sprintf("ghs_token_%d", callCount),
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	defer server.Close()

	auth.baseURL = server.URL

	// First call should hit the server
	token1, err := auth.Token()
	if err != nil {
		t.Fatalf("first Token() call: %v", err)
	}

	// Second call should return cached token
	token2, err := auth.Token()
	if err != nil {
		t.Fatalf("second Token() call: %v", err)
	}

	if token1 != token2 {
		t.Errorf("expected cached token %q, got %q", token1, token2)
	}
	if callCount != 1 {
		t.Errorf("expected 1 server call, got %d", callCount)
	}
}

func TestGitHubAppAuth_Token_RefreshesExpiredToken(t *testing.T) {
	_, pemBytes := generateTestKey(t)
	auth, err := NewGitHubAppAuth("42", "99", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      fmt.Sprintf("ghs_token_%d", callCount),
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	defer server.Close()

	auth.baseURL = server.URL

	// Get initial token
	_, err = auth.Token()
	if err != nil {
		t.Fatalf("first Token() call: %v", err)
	}

	// Simulate expired token (set expiry in the past)
	auth.mu.Lock()
	auth.tokenExpiry = time.Now().Add(-1 * time.Minute)
	auth.mu.Unlock()

	// Should fetch a new token
	token2, err := auth.Token()
	if err != nil {
		t.Fatalf("second Token() call: %v", err)
	}

	if token2 != "ghs_token_2" {
		t.Errorf("expected refreshed token %q, got %q", "ghs_token_2", token2)
	}
	if callCount != 2 {
		t.Errorf("expected 2 server calls, got %d", callCount)
	}
}

func TestGitHubAppAuth_Token_ServerError(t *testing.T) {
	_, pemBytes := generateTestKey(t)
	auth, err := NewGitHubAppAuth("42", "99", pemBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"internal error"}`))
	}))
	defer server.Close()

	auth.baseURL = server.URL

	_, err = auth.Token()
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		n        int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"  spaces  ", 10, "spaces"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.expected)
		}
	}
}
