package scraper

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GitHubAppAuth handles GitHub App authentication by generating JWTs
// from a private key and exchanging them for installation access tokens.
type GitHubAppAuth struct {
	appID          string
	installationID string
	privateKey     *rsa.PrivateKey
	httpClient     *http.Client
	baseURL        string

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

// NewGitHubAppAuth creates a GitHubAppAuth from the given app ID,
// installation ID, and PEM-encoded private key.
func NewGitHubAppAuth(appID, installationID string, privateKeyPEM []byte) (*GitHubAppAuth, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from private key")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("failed to parse private key (PKCS1: %v, PKCS8: %v)", err, err2)
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
	}

	return &GitHubAppAuth{
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		baseURL:        defaultGitHubBaseURL,
	}, nil
}

// Token returns a valid installation access token, refreshing if needed.
func (a *GitHubAppAuth) Token() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Return cached token if still valid (with 5-minute buffer)
	if a.cachedToken != "" && time.Now().Before(a.tokenExpiry.Add(-5*time.Minute)) {
		return a.cachedToken, nil
	}

	jwt, err := a.createJWT()
	if err != nil {
		return "", fmt.Errorf("creating JWT: %w", err)
	}

	token, expiry, err := a.exchangeForInstallationToken(jwt)
	if err != nil {
		return "", fmt.Errorf("exchanging JWT for installation token: %w", err)
	}

	a.cachedToken = token
	a.tokenExpiry = expiry
	return token, nil
}

// createJWT creates a signed JWT for GitHub App authentication.
func (a *GitHubAppAuth) createJWT() (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]interface{}{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": a.appID,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, a.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64, nil
}

// exchangeForInstallationToken exchanges a JWT for an installation access token.
func (a *GitHubAppAuth) exchangeForInstallationToken(jwt string) (string, time.Time, error) {
	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", a.baseURL, a.installationID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("failed to create installation token: HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tokenResp struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding token response: %w", err)
	}

	return tokenResp.Token, tokenResp.ExpiresAt, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
