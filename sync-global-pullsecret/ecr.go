package syncglobalpullsecret

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/smithy-go"

	"github.com/go-logr/logr"
)

const (
	// ecrRefreshInterval is how often to refresh ECR credentials
	// ECR tokens expire in 12 hours, so refresh every 6 hours to ensure availability
	ecrRefreshInterval = 6 * time.Hour

	// ecrCacheCheckBuffer is how long before expiration to consider a credential invalid
	// Check cache validity 1 hour before expiration for safety
	ecrCacheCheckBuffer = 1 * time.Hour
)

//go:generate ../hack/tools/bin/mockgen -destination=ecr_mock.go -package=syncglobalpullsecret . ecrClient
type ecrClient interface {
	GetAuthorizationToken(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

// awsECRClient wraps the AWS SDK v2 ECR client
type awsECRClient struct {
	client *ecr.Client
}

func (c *awsECRClient) GetAuthorizationToken(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	return c.client.GetAuthorizationToken(ctx, params, optFns...)
}

// ecrCredentialCache holds cached ECR credentials with expiration times
type ecrCredentialCache struct {
	mu          sync.RWMutex
	credentials map[string]*cachedCredential
}

// cachedCredential represents a single cached ECR credential
type cachedCredential struct {
	authToken string    // Base64-encoded ECR auth token
	expiresAt time.Time // Token expiration time
}

// isValid checks if the cached credential exists and hasn't expired
// Returns true if the credential is valid and won't expire within ecrCacheCheckBuffer
func (c *cachedCredential) isValid() bool {
	// Refresh 1 hour before expiration to ensure availability
	return time.Now().Add(ecrCacheCheckBuffer).Before(c.expiresAt)
}

// newECRClient creates an ECR client using EC2 instance credentials with IMDSv2
func newECRClient(ctx context.Context, log logr.Logger) (ecrClient, error) {
	// Load AWS config with IMDSv2 enabled by default
	// AWS SDK v2 uses IMDSv2 by default (EC2IMDSv1Disabled: true in ec2rolecreds)
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithEC2IMDSRegion(), // Auto-detect region from IMDS
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Verify we're running on EC2 by checking IMDS availability
	// Use a short timeout for IMDS availability check
	imdsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	imdsClient := imds.NewFromConfig(cfg)
	_, err = imdsClient.GetMetadata(imdsCtx, &imds.GetMetadataInput{
		Path: "instance-id",
	})
	if err != nil {
		return nil, fmt.Errorf("not running on AWS EC2 instance (IMDS unavailable): %w", err)
	}

	log.Info("Successfully initialized ECR client with IMDSv2")
	return &awsECRClient{
		client: ecr.NewFromConfig(cfg),
	}, nil
}

// fetchECRCredentials fetches ECR authorization tokens for all configured registries
func (s *GlobalPullSecretSyncer) fetchECRCredentials(ctx context.Context) (map[string]string, error) {
	if len(s.ecrRegistries) == 0 {
		return nil, nil
	}

	s.log.Info("Fetching ECR credentials", "registryCount", len(s.ecrRegistries))

	// Call ECR GetAuthorizationToken API without RegistryIds (deprecated)
	// This returns a token that works for all ECR registries in the same AWS account
	output, err := s.ecrClient.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		// Check if rate limited
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ThrottlingException" {
			s.log.Info("ECR API rate limited, using cached credentials")
			return s.getCachedCredentials(), nil
		}
		return nil, fmt.Errorf("failed to get ECR authorization token: %w", err)
	}

	// Verify we got authorization data
	if len(output.AuthorizationData) == 0 {
		return nil, fmt.Errorf("no authorization data returned from ECR")
	}

	// Get the authorization token (works for all registries in the account)
	authData := output.AuthorizationData[0]
	if authData.AuthorizationToken == nil || authData.ExpiresAt == nil {
		return nil, fmt.Errorf("invalid authorization data: missing token or expiration")
	}

	s.log.Info("Fetched ECR credential", "expiresAt", authData.ExpiresAt)

	// Apply the token to all configured registries
	credentials := make(map[string]string)
	s.ecrCredCache.mu.Lock()
	for _, registry := range s.ecrRegistries {
		// Validate registry before caching credentials
		if err := validateECRRegistry(registry); err != nil {
			s.log.Error(err, "Skipping invalid ECR registry", "registry", registry)
			continue
		}

		credentials[registry] = *authData.AuthorizationToken

		// Update cache with the same token for each configured registry
		s.ecrCredCache.credentials[registry] = &cachedCredential{
			authToken: *authData.AuthorizationToken,
			expiresAt: *authData.ExpiresAt,
		}
		s.log.Info("Applied ECR credential to registry", "registry", registry)
	}
	s.ecrCredCache.mu.Unlock()

	return credentials, nil
}

// getCachedCredentials returns all cached credentials as a map
func (s *GlobalPullSecretSyncer) getCachedCredentials() map[string]string {
	s.ecrCredCache.mu.RLock()
	defer s.ecrCredCache.mu.RUnlock()

	creds := make(map[string]string)
	for registry, cred := range s.ecrCredCache.credentials {
		creds[registry] = cred.authToken
	}
	return creds
}

// buildDockerConfigWithECR merges ECR credentials with existing pull secret
func (s *GlobalPullSecretSyncer) buildDockerConfigWithECR(basePullSecretBytes []byte) ([]byte, error) {
	// Check cache length under read lock
	s.ecrCredCache.mu.RLock()
	cacheIsEmpty := len(s.ecrCredCache.credentials) == 0
	s.ecrCredCache.mu.RUnlock()

	if cacheIsEmpty {
		return basePullSecretBytes, nil
	}

	s.log.Info("Merging ECR credentials with pull secret")

	// Parse base pull secret
	var baseConfig map[string]any
	if err := json.Unmarshal(basePullSecretBytes, &baseConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal base pull secret: %w", err)
	}

	auths, ok := baseConfig["auths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("base pull secret missing 'auths' key")
	}

	// Add ECR credentials
	s.ecrCredCache.mu.RLock()
	defer s.ecrCredCache.mu.RUnlock()

	for registry, cred := range s.ecrCredCache.credentials {
		if !cred.isValid() {
			s.log.Info("Skipping expired ECR credential", "registry", registry)
			continue
		}

		// Check if registry already exists in base config
		if _, exists := auths[registry]; exists {
			s.log.Info("Registry already exists in base pull secret, preserving original credential", "registry", registry)
			continue
		}

		// Parse ECR token
		username, password, err := parseECRToken(cred.authToken)
		if err != nil {
			s.log.Error(err, "Failed to parse ECR token", "registry", registry)
			continue
		}

		// Create Docker config auth entry
		authEntry := map[string]any{
			"username": username,
			"password": password,
			"auth":     cred.authToken, // ECR token is already base64-encoded
		}

		auths[registry] = authEntry
		s.log.Info("Added ECR credential to pull secret", "registry", registry)
	}

	// Marshal final config
	finalConfig := map[string]any{
		"auths": auths,
	}

	finalBytes, err := json.Marshal(finalConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}

	return finalBytes, nil
}

// parseECRToken converts ECR base64 token to username/password
// ECR token format: base64("AWS:<password>")
func parseECRToken(token string) (username, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode ECR token: %w", err)
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 || parts[0] != "AWS" {
		return "", "", fmt.Errorf("invalid ECR token format")
	}

	return "AWS", parts[1], nil
}

// validateECRRegistry ensures registry URL is valid ECR format
func validateECRRegistry(registry string) error {
	// Expected format: <account-id>.dkr.ecr.<region>.amazonaws.com
	parts := strings.Split(registry, ".")
	if len(parts) != 6 {
		return fmt.Errorf("expected format: <account>.dkr.ecr.<region>.amazonaws.com")
	}
	if parts[1] != "dkr" || parts[2] != "ecr" {
		return fmt.Errorf("not a valid ECR registry URL")
	}
	if parts[4] != "amazonaws" || parts[5] != "com" {
		return fmt.Errorf("not an AWS ECR registry")
	}
	return nil
}
