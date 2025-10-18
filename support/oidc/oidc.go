package oidc

import (
	"bytes"
	"crypto"
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

func GenerateJWKSDocument(params OIDCGeneratorParams) (io.ReadSeeker, error) {
	block, _ := pem.Decode(params.PubKey)
	if block == nil || block.Type != "RSA PUBLIC KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing RSA public key")
	}
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}
	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}

	hasher := crypto.SHA256.New()
	hasher.Write(block.Bytes)
	hash := hasher.Sum(nil)
	kid := base64.RawURLEncoding.EncodeToString(hash)

	var keys []jose.JSONWebKey
	keys = append(keys, jose.JSONWebKey{
		Key:       rsaPubKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
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
		"RS256"
	]
}`
)

func GenerateConfigurationDocument(params OIDCGeneratorParams) (io.ReadSeeker, error) {
	return strings.NewReader(fmt.Sprintf(discoveryTemplate, params.IssuerURL, params.IssuerURL, JWKSURI)), nil
}
