package oidc

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"strings"

	"github.com/go-jose/go-jose/v3"
)

type OIDCGeneratorParams struct {
	IssuerURL string
	PubKey    []byte
}

type KeyResponse struct {
	Keys []jose.JSONWebKey `json:"keys"`
}

type OIDCDocumentGeneratorFunc func(params OIDCGeneratorParams) (io.ReadSeeker, error)

func algorithmFromPublicKey(pub crypto.PublicKey) (jose.SignatureAlgorithm, error) {
	switch pk := pub.(type) {
	case *rsa.PublicKey:
		return jose.RS256, nil
	case *ecdsa.PublicKey:
		switch pk.Curve {
		case elliptic.P256():
			return jose.ES256, nil
		case elliptic.P384():
			return jose.ES384, nil
		case elliptic.P521():
			return jose.ES512, nil
		default:
			return "", fmt.Errorf("unsupported ECDSA curve %v", pk.Curve.Params().Name)
		}
	default:
		return "", fmt.Errorf("unsupported key type %T", pub)
	}
}

func parsePublicKeyAndAlgorithm(pemBytes []byte) (crypto.PublicKey, jose.SignatureAlgorithm, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, "", fmt.Errorf("failed to decode PEM block containing public key")
	}
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse public key: %w", err)
	}
	alg, err := algorithmFromPublicKey(pubKey)
	if err != nil {
		return nil, "", err
	}
	return pubKey, alg, nil
}

func GenerateJWKSDocument(params OIDCGeneratorParams) (io.ReadSeeker, error) {
	pubKey, alg, err := parsePublicKeyAndAlgorithm(params.PubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to determine signing algorithm: %w", err)
	}

	block, _ := pem.Decode(params.PubKey)
	hasher := crypto.SHA256.New()
	hasher.Write(block.Bytes)
	hash := hasher.Sum(nil)
	kid := base64.RawURLEncoding.EncodeToString(hash)

	var keys []jose.JSONWebKey
	keys = append(keys, jose.JSONWebKey{
		Key:       pubKey,
		KeyID:     kid,
		Algorithm: string(alg),
		Use:       "sig",
	})

	jwks, err := json.MarshalIndent(KeyResponse{Keys: keys}, "", "  ")
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(jwks), nil
}

const (
	JWKSURI           = "/openid/v1/jwks"
	discoveryTemplate = `{
	"issuer": "%s",
	"jwks_uri": "%s%s",
	"response_types_supported": [
		"id_token"
	],
	"subject_types_supported": [
		"public"
	],
	"id_token_signing_alg_values_supported": [
		"%s"
	]
}`
)

func GenerateConfigurationDocument(params OIDCGeneratorParams) (io.ReadSeeker, error) {
	alg := "RS256"
	if len(params.PubKey) > 0 {
		_, detectedAlg, err := parsePublicKeyAndAlgorithm(params.PubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to determine signing algorithm: %w", err)
		}
		alg = string(detectedAlg)
	}
	return strings.NewReader(fmt.Sprintf(discoveryTemplate, params.IssuerURL, params.IssuerURL, JWKSURI, alg)), nil
}
