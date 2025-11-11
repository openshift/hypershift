package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

const (
	// defaultOIDCAudience is the default audience for OIDC providers.
	defaultOIDCAudience = "openshift"

	// defaultOperationTimeout is the timeout for waiting on GCP long-running operations.
	defaultOperationTimeout = 5 * time.Minute

	// defaultPollingInterval is the interval between polling attempts for operation status.
	defaultPollingInterval = 2 * time.Second
)

// IAMManager encapsulates all GCP API interactions used by the GCP IAM creation flow.
type IAMManager struct {
	projectID     string
	projectNumber string
	infraID       string
	oidcIssuerURL string
	jwksFile      string

	iamService *iam.Service
	crmService *cloudresourcemanager.Service
	logger     logr.Logger
}

func NewIAMManager(ctx context.Context, projectID string, infraID string, jwksFile string, logger logr.Logger) (*IAMManager, error) {
	iamService, err := iam.NewService(ctx, option.WithScopes(iam.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM service client: %w", err)
	}
	crmService, err := cloudresourcemanager.NewService(ctx, option.WithScopes(cloudresourcemanager.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Resource Manager service client: %w", err)
	}

	if infraID == "" {
		return nil, fmt.Errorf("infraID is not set on Client")
	}
	if jwksFile == "" {
		return nil, fmt.Errorf("jwksFile is required")
	}

	if projectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}
	return &IAMManager{
		projectID:  projectID,
		infraID:    infraID,
		jwksFile:   jwksFile,
		iamService: iamService,
		crmService: crmService,
		logger:     logger,
	}, nil
}

func (c *IAMManager) GetProjectNumber(ctx context.Context) (string, error) {
	if c.projectNumber != "" {
		c.logger.Info("Using existing project number", "projectID", c.projectID, "projectNumber", c.projectNumber)
		return c.projectNumber, nil
	}
	c.logger.Info("Retrieving project number", "projectID", c.projectID)

	projectNumber, err := c.getProjectNumberFromID(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve project number for %s: %w", c.projectID, err)
	}

	c.logger.Info("Successfully retrieved project number", "projectNumber", projectNumber)

	c.projectNumber = fmt.Sprintf("%d", projectNumber)
	return c.projectNumber, nil
}

func (c *IAMManager) CreateWorkloadIdentityPool(ctx context.Context) (string, error) {

	poolID := c.formatPoolID()
	c.logger.Info("Creating Workload Identity Pool", "poolID", poolID, "projectID", c.projectID)

	pool := &iam.WorkloadIdentityPool{
		Description: fmt.Sprintf("Workload Identity Pool for HyperShift cluster %s", c.infraID),
		DisplayName: poolID,
		Disabled:    false,
	}
	parent := fmt.Sprintf("projects/%s/locations/global", c.projectID)
	err := c.createWorkloadIdentityPool(ctx, parent, poolID, pool)
	if err != nil {
		if isAlreadyExistsError(err) {
			c.logger.Info("Workload Identity Pool already exists", "poolID", poolID)
		} else {
			return "", fmt.Errorf("failed to create Workload Identity Pool: %w", err)
		}
	} else {
		c.logger.Info("Successfully created Workload Identity Pool", "poolID", poolID)
	}
	return poolID, nil
}

func (c *IAMManager) CreateOIDCProvider(ctx context.Context) (string, string, error) {

	providerID := c.formatProviderID()
	c.logger.Info("Creating OIDC Provider", "providerID", providerID, "poolID", c.formatPoolID())

	jwksData, err := os.ReadFile(c.jwksFile)
	if err != nil {
		return "", "", fmt.Errorf("failed to read JWKS file: %w", err)
	}
	// Basic JSON validation
	var js map[string]any
	if err := json.Unmarshal(jwksData, &js); err != nil {
		return "", "", fmt.Errorf("JWKS file contains invalid JSON: %w", err)
	}

	providerAudience := c.formatProviderAudience()
	provider := &iam.WorkloadIdentityPoolProvider{
		Description: fmt.Sprintf("OIDC Provider for HyperShift cluster %s", c.infraID),
		DisplayName: providerID,
		Disabled:    false,
		// JWKS is sufficient for the provider;
		// Valid OIDC issuer URL option is only relevant when JWKS is not provided.
		// In this case, the issuer URL will be derived from infraID if not provided.
		Oidc: &iam.Oidc{
			AllowedAudiences: []string{defaultOIDCAudience},
			IssuerUri:        c.formatIssuerUri(),
			JwksJson:         string(jwksData),
		},
		AttributeMapping: map[string]string{
			"google.subject": "assertion.sub",
		},
	}
	parent := fmt.Sprintf("projects/%s/locations/global/workloadIdentityPools/%s", c.projectID, c.formatPoolID())
	err = c.createWorkloadIdentityProvider(ctx, parent, providerID, provider)
	if err != nil {
		if isAlreadyExistsError(err) {
			c.logger.Info("OIDC Provider already exists", "providerID", providerID)
		} else {
			return "", "", fmt.Errorf("failed to create OIDC Provider: %w", err)
		}
	} else {
		c.logger.Info("Successfully created OIDC Provider", "providerID", providerID)
	}

	// Set providerAudience including project number
	c.logger.Info("Successfully created OIDC Provider", "providerID", providerID, "audience", providerAudience)
	return providerID, providerAudience, nil
}

func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 409
	}
	return false
}

func (c *IAMManager) getProjectNumberFromID(ctx context.Context) (int64, error) {
	project, err := c.crmService.Projects.Get(c.projectID).Do()
	if err != nil {
		return 0, err
	}
	return project.ProjectNumber, err
}

func (c *IAMManager) createWorkloadIdentityPool(
	ctx context.Context,
	parent, poolID string,
	pool *iam.WorkloadIdentityPool,
) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.
		Create(parent, pool).
		WorkloadIdentityPoolId(poolID).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}
	if err := c.waitOperation(ctx, op.Name); err != nil {
		return err
	}
	return nil
}

func (c *IAMManager) createWorkloadIdentityProvider(
	ctx context.Context,
	parent, providerID string,
	provider *iam.WorkloadIdentityPoolProvider,
) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.
		Create(parent, provider).
		WorkloadIdentityPoolProviderId(providerID).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// waitOperation polls a long-running operation until completion or timeout.
func (c *IAMManager) waitOperation(ctx context.Context, opName string) error {
	deadline := time.Now().Add(defaultOperationTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("operation timed out: %s", opName)
		}
		op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Operations.Get(opName).Context(ctx).Do()
		if err != nil {
			return err
		}
		if op.Done {
			if op.Error != nil {
				return fmt.Errorf("operation error: %v", op.Error)
			}
			return nil
		}
		time.Sleep(defaultPollingInterval)
	}
}

// formatPoolID returns the Workload Identity Pool ID derived from infraID.
func (c *IAMManager) formatPoolID() string {
	return fmt.Sprintf("%s-wi-pool", c.infraID)
}

// formatProviderID returns the Workload Identity Provider ID derived from infraID.
func (c *IAMManager) formatProviderID() string {
	return fmt.Sprintf("%s-k8s-provider", c.infraID)
}

// formatProviderAudience returns the fully-qualified provider audience.
func (c *IAMManager) formatProviderAudience() string {
	return fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
		c.projectNumber, c.formatPoolID(), c.formatProviderID())
}

// SetOIDCIssuerURL sets a custom OIDC issuer URL.
func (c *IAMManager) SetOIDCIssuerURL(url string) {
	c.oidcIssuerURL = url
}

// formatIssuerUri returns the OIDC issuer URI.
// If a custom URL is set, it returns that; otherwise, it derives from infraID.
func (c *IAMManager) formatIssuerUri() string {
	if c.oidcIssuerURL != "" {
		return c.oidcIssuerURL
	}
	return fmt.Sprintf("https://hypershift-%s-oidc", c.infraID)
}
