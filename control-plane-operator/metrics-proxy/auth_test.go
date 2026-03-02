package metricsproxy

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		token               string
		authorizedSAs       []string
		tokenReviewResponse string
		setupCache          func(*TokenAuthenticator)
		expectedAuth        bool
		expectedError       bool
		expectCacheHit      bool
	}{
		{
			name:  "When token is cached for an authorized SA and not expired, it should return true without calling tokenReview",
			token: "valid-cached-token",
			setupCache: func(a *TokenAuthenticator) {
				a.cache["system:serviceaccount:kube-system:prometheus"] = cachedAuthResult{
					tokenHash: hashToken("valid-cached-token"),
					expiry:    time.Now().Add(5 * time.Minute),
				}
			},
			expectedAuth:   true,
			expectedError:  false,
			expectCacheHit: true,
		},
		{
			name:  "When token is cached but expired, it should call tokenReview",
			token: "expired-cached-token",
			authorizedSAs: []string{
				"system:serviceaccount:kube-system:prometheus",
			},
			setupCache: func(a *TokenAuthenticator) {
				a.cache["system:serviceaccount:kube-system:prometheus"] = cachedAuthResult{
					tokenHash: hashToken("expired-cached-token"),
					expiry:    time.Now().Add(-1 * time.Minute),
				}
			},
			tokenReviewResponse: `{
				"apiVersion": "authentication.k8s.io/v1",
				"kind": "TokenReview",
				"status": {
					"authenticated": true,
					"user": {
						"username": "system:serviceaccount:kube-system:prometheus"
					}
				}
			}`,
			expectedAuth:   true,
			expectedError:  false,
			expectCacheHit: false,
		},
		{
			name:  "When a different token is presented, it should call tokenReview and replace the cached entry for that SA",
			token: "new-token",
			authorizedSAs: []string{
				"system:serviceaccount:kube-system:prometheus",
			},
			setupCache: func(a *TokenAuthenticator) {
				a.cache["system:serviceaccount:kube-system:prometheus"] = cachedAuthResult{
					tokenHash: hashToken("old-token"),
					expiry:    time.Now().Add(5 * time.Minute),
				}
			},
			tokenReviewResponse: `{
				"apiVersion": "authentication.k8s.io/v1",
				"kind": "TokenReview",
				"status": {
					"authenticated": true,
					"user": {
						"username": "system:serviceaccount:kube-system:prometheus"
					}
				}
			}`,
			expectedAuth:   true,
			expectedError:  false,
			expectCacheHit: false,
		},
		{
			name:  "When tokenReview succeeds for authorized SA, it should cache and return true",
			token: "valid-token",
			authorizedSAs: []string{
				"system:serviceaccount:kube-system:prometheus",
			},
			tokenReviewResponse: `{
				"apiVersion": "authentication.k8s.io/v1",
				"kind": "TokenReview",
				"status": {
					"authenticated": true,
					"user": {
						"username": "system:serviceaccount:kube-system:prometheus"
					}
				}
			}`,
			expectedAuth:   true,
			expectedError:  false,
			expectCacheHit: false,
		},
		{
			name:  "When tokenReview fails authentication, it should not cache and return false",
			token: "invalid-token",
			authorizedSAs: []string{
				"system:serviceaccount:kube-system:prometheus",
			},
			tokenReviewResponse: `{
				"apiVersion": "authentication.k8s.io/v1",
				"kind": "TokenReview",
				"status": {
					"authenticated": false
				}
			}`,
			expectedAuth:   false,
			expectedError:  false,
			expectCacheHit: false,
		},
		{
			name:  "When authenticated user is not in authorized SA list, it should not cache and return false",
			token: "unauthorized-sa-token",
			authorizedSAs: []string{
				"system:serviceaccount:kube-system:prometheus",
			},
			tokenReviewResponse: `{
				"apiVersion": "authentication.k8s.io/v1",
				"kind": "TokenReview",
				"status": {
					"authenticated": true,
					"user": {
						"username": "system:serviceaccount:default:unauthorized"
					}
				}
			}`,
			expectedAuth:   false,
			expectedError:  false,
			expectCacheHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var kasCallCount int
			kasServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				kasCallCount++
				w.Header().Set("Content-Type", "application/json")
				if tt.tokenReviewResponse != "" {
					if _, err := w.Write([]byte(tt.tokenReviewResponse)); err != nil {
						t.Errorf("failed to write response: %v", err)
					}
				}
			}))
			defer kasServer.Close()

			caPEM := pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: kasServer.Certificate().Raw,
			})

			kasClient, err := kubernetes.NewForConfig(&rest.Config{
				Host: kasServer.URL,
				TLSClientConfig: rest.TLSClientConfig{
					CAData: caPEM,
				},
			})
			if err != nil {
				t.Fatalf("Failed to create KAS client: %v", err)
			}

			authenticator := NewTokenAuthenticator(kasClient, tt.authorizedSAs)
			if tt.setupCache != nil {
				tt.setupCache(authenticator)
			}

			ctx := context.Background()
			authenticated, err := authenticator.Authenticate(ctx, tt.token)

			if tt.expectedError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if authenticated != tt.expectedAuth {
				t.Errorf("Expected authenticated=%v, got %v", tt.expectedAuth, authenticated)
			}

			if tt.expectCacheHit && kasCallCount > 0 {
				t.Error("Expected cache hit (no KAS call) but KAS was called")
			}
			if !tt.expectCacheHit && kasCallCount == 0 {
				t.Error("Expected KAS call (cache miss) but KAS was not called")
			}
		})
	}
}

func TestHashToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		token1        string
		token2        string
		shouldBeEqual bool
	}{
		{
			name:          "When same token is hashed twice, it should produce same hash",
			token1:        "my-secret-token",
			token2:        "my-secret-token",
			shouldBeEqual: true,
		},
		{
			name:          "When different tokens are hashed, it should produce different hashes",
			token1:        "token-one",
			token2:        "token-two",
			shouldBeEqual: false,
		},
		{
			name:          "When empty strings are hashed, it should produce same hash",
			token1:        "",
			token2:        "",
			shouldBeEqual: true,
		},
		{
			name:          "When similar tokens are hashed, it should produce different hashes",
			token1:        "token",
			token2:        "token ",
			shouldBeEqual: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := hashToken(tt.token1)
			hash2 := hashToken(tt.token2)

			hash1Repeat := hashToken(tt.token1)
			if hash1 != hash1Repeat {
				t.Error("Hash function is not deterministic")
			}

			expectedLen := 64 // SHA256 = 32 bytes = 64 hex characters
			if len(hash1) != expectedLen {
				t.Errorf("Expected hash length %d, got %d", expectedLen, len(hash1))
			}

			if tt.shouldBeEqual {
				if hash1 != hash2 {
					t.Errorf("Expected hashes to be equal, got %s and %s", hash1, hash2)
				}
			} else {
				if hash1 == hash2 {
					t.Errorf("Expected hashes to be different, but both are %s", hash1)
				}
			}
		})
	}
}
