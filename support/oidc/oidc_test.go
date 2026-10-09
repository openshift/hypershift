package oidc

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"testing"

	"github.com/go-jose/go-jose/v3"
)

func pemEncodePublicKey(t *testing.T, pub interface{}, blockType string) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func TestAlgorithmFromPublicKey(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		keyFunc func(t *testing.T) interface{}
		wantAlg jose.SignatureAlgorithm
		wantErr bool
	}{
		{
			name: "When given an RSA key, it should return RS256",
			keyFunc: func(t *testing.T) interface{} {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatalf("failed to generate RSA key: %v", err)
				}
				return &key.PublicKey
			},
			wantAlg: jose.RS256,
		},
		{
			name: "When given an ECDSA P-256 key, it should return ES256",
			keyFunc: func(t *testing.T) interface{} {
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return &key.PublicKey
			},
			wantAlg: jose.ES256,
		},
		{
			name: "When given an ECDSA P-384 key, it should return ES384",
			keyFunc: func(t *testing.T) interface{} {
				key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return &key.PublicKey
			},
			wantAlg: jose.ES384,
		},
		{
			name: "When given an ECDSA P-521 key, it should return ES512",
			keyFunc: func(t *testing.T) interface{} {
				key, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return &key.PublicKey
			},
			wantAlg: jose.ES512,
		},
		{
			name: "When given an unsupported key type, it should return an error",
			keyFunc: func(t *testing.T) interface{} {
				pub, _, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate Ed25519 key: %v", err)
				}
				return pub
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pub := tc.keyFunc(t)
			alg, err := algorithmFromPublicKey(pub)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if alg != tc.wantAlg {
				t.Errorf("got algorithm %q, want %q", alg, tc.wantAlg)
			}
		})
	}
}

func TestGenerateJWKSDocument(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		keyFunc func(t *testing.T) []byte
		wantKty string
		wantAlg string
	}{
		{
			name: "When given an RSA key, it should produce an RSA JWK with RS256",
			keyFunc: func(t *testing.T) []byte {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatalf("failed to generate RSA key: %v", err)
				}
				return pemEncodePublicKey(t, &key.PublicKey, "PUBLIC KEY")
			},
			wantKty: "RSA",
			wantAlg: "RS256",
		},
		{
			name: "When given an ECDSA P-256 key, it should produce an EC JWK with ES256",
			keyFunc: func(t *testing.T) []byte {
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return pemEncodePublicKey(t, &key.PublicKey, "PUBLIC KEY")
			},
			wantKty: "EC",
			wantAlg: "ES256",
		},
		{
			name: "When given an ECDSA P-384 key, it should produce an EC JWK with ES384",
			keyFunc: func(t *testing.T) []byte {
				key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return pemEncodePublicKey(t, &key.PublicKey, "PUBLIC KEY")
			},
			wantKty: "EC",
			wantAlg: "ES384",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pubKeyPEM := tc.keyFunc(t)
			params := OIDCGeneratorParams{
				IssuerURL: "https://example.com",
				PubKey:    pubKeyPEM,
			}
			reader, err := GenerateJWKSDocument(params)
			if err != nil {
				t.Fatalf("GenerateJWKSDocument failed: %v", err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("failed to read JWKS: %v", err)
			}

			var resp KeyResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				t.Fatalf("failed to unmarshal JWKS: %v", err)
			}
			if len(resp.Keys) != 1 {
				t.Fatalf("expected 1 key, got %d", len(resp.Keys))
			}
			jwk := resp.Keys[0]
			if jwk.Algorithm != tc.wantAlg {
				t.Errorf("got algorithm %q, want %q", jwk.Algorithm, tc.wantAlg)
			}
			if jwk.Use != "sig" {
				t.Errorf("got use %q, want %q", jwk.Use, "sig")
			}
			if jwk.KeyID == "" {
				t.Error("expected non-empty key ID")
			}

			rawJSON, err := json.Marshal(jwk)
			if err != nil {
				t.Fatalf("failed to marshal JWK: %v", err)
			}
			var fields map[string]interface{}
			if err := json.Unmarshal(rawJSON, &fields); err != nil {
				t.Fatalf("failed to unmarshal JWK fields: %v", err)
			}
			if got := fields["kty"].(string); got != tc.wantKty {
				t.Errorf("got kty %q, want %q", got, tc.wantKty)
			}
		})
	}
}

func TestGenerateJWKSDocumentAcceptsBothPEMHeaders(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	testCases := []struct {
		name      string
		blockType string
	}{
		{
			name:      "When given a PUBLIC KEY header, it should accept the key",
			blockType: "PUBLIC KEY",
		},
		{
			name:      "When given an RSA PUBLIC KEY header, it should accept the key",
			blockType: "RSA PUBLIC KEY",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pubPEM := pemEncodePublicKey(t, &key.PublicKey, tc.blockType)
			params := OIDCGeneratorParams{
				IssuerURL: "https://example.com",
				PubKey:    pubPEM,
			}
			_, err := GenerateJWKSDocument(params)
			if err != nil {
				t.Fatalf("GenerateJWKSDocument rejected PEM header %q: %v", tc.blockType, err)
			}
		})
	}
}

func TestGenerateConfigurationDocument(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		keyFunc func(t *testing.T) []byte
		wantAlg string
	}{
		{
			name: "When given an RSA key, it should advertise RS256",
			keyFunc: func(t *testing.T) []byte {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatalf("failed to generate RSA key: %v", err)
				}
				return pemEncodePublicKey(t, &key.PublicKey, "PUBLIC KEY")
			},
			wantAlg: "RS256",
		},
		{
			name: "When given an ECDSA P-256 key, it should advertise ES256",
			keyFunc: func(t *testing.T) []byte {
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatalf("failed to generate ECDSA key: %v", err)
				}
				return pemEncodePublicKey(t, &key.PublicKey, "PUBLIC KEY")
			},
			wantAlg: "ES256",
		},
		{
			name:    "When PubKey is empty, it should default to RS256",
			keyFunc: func(t *testing.T) []byte { return nil },
			wantAlg: "RS256",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			params := OIDCGeneratorParams{
				IssuerURL: "https://example.com",
				PubKey:    tc.keyFunc(t),
			}
			reader, err := GenerateConfigurationDocument(params)
			if err != nil {
				t.Fatalf("GenerateConfigurationDocument failed: %v", err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("failed to read config: %v", err)
			}

			var config struct {
				Issuer  string   `json:"issuer"`
				JWKSURI string   `json:"jwks_uri"`
				Algs    []string `json:"id_token_signing_alg_values_supported"`
			}
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatalf("failed to unmarshal config: %v", err)
			}
			if len(config.Algs) != 1 {
				t.Fatalf("expected 1 algorithm, got %d", len(config.Algs))
			}
			if config.Algs[0] != tc.wantAlg {
				t.Errorf("got algorithm %q, want %q", config.Algs[0], tc.wantAlg)
			}
		})
	}
}
