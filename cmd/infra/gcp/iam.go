package gcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

//go:embed iam-bindings.json
var defaultServiceAccountsJSON []byte

const (
	// defaultOIDCAudience is the default audience for OIDC providers.
	defaultOIDCAudience = "openshift"

	// defaultOperationTimeout is the timeout for waiting on GCP long-running operations.
	defaultOperationTimeout = 5 * time.Minute

	// defaultPollingInterval is the interval between polling attempts for operation status.
	defaultPollingInterval = 2 * time.Second

	// iamPropagationTimeout is the maximum time to wait for IAM eventual consistency.
	// GCP IAM changes typically propagate in 5-30 seconds, we allow 60 seconds to be safe.
	iamPropagationTimeout = 60 * time.Second

	// iamPropagationInitialBackoff is the initial backoff duration for IAM retry operations.
	iamPropagationInitialBackoff = 2 * time.Second

	// iamPropagationMaxBackoff is the maximum backoff duration for IAM retry operations.
	iamPropagationMaxBackoff = 16 * time.Second

	// workloadIdentityUserRole is the IAM role that allows WIF principal to impersonate a GSA.
	workloadIdentityUserRole = "roles/iam.workloadIdentityUser"
)

// ServiceAccountDefinition defines a Google Service Account to be created and its role bindings.
type ServiceAccountDefinition struct {
	// Name is the component name (used for GSA email: {infraID}-{Name}@{project}.iam.gserviceaccount.com)
	Name string `json:"name"`

	// DisplayName is the human-readable name for the GSA
	DisplayName string `json:"displayName"`

	// Description provides context about the GSA's purpose
	Description string `json:"description"`

	// Roles are the GCP IAM roles to assign to this GSA
	Roles []string `json:"roles"`

	// K8sServiceAccount contains the namespace and name of the K8s SA for WIF binding
	K8sServiceAccount *K8sServiceAccountRef `json:"k8sServiceAccount,omitempty"`
}

// K8sServiceAccountRef identifies a Kubernetes ServiceAccount for WIF binding.
type K8sServiceAccountRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ServiceAccountsConfig is the root structure for the service accounts JSON file.
type ServiceAccountsConfig struct {
	ServiceAccounts []ServiceAccountDefinition `json:"serviceAccounts"`
}

// loadServiceAccountDefinitions loads and parses the service accounts configuration.
// It uses the embedded JSON file by default, or can load from a custom file if provided.
func loadServiceAccountDefinitions(customConfigPath string) ([]ServiceAccountDefinition, error) {
	var data []byte
	var err error

	if customConfigPath != "" {
		// Load from custom file path
		data, err = os.ReadFile(customConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read custom service accounts config: %w", err)
		}
	} else {
		// Use embedded default configuration
		data = defaultServiceAccountsJSON
	}

	var config ServiceAccountsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse service accounts configuration: %w", err)
	}

	if len(config.ServiceAccounts) == 0 {
		return nil, fmt.Errorf("service accounts configuration is empty")
	}

	return config.ServiceAccounts, nil
}

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
			// Pool exists, check and fix its state if needed
			c.logger.Info("Workload Identity Pool already exists, checking state", "poolID", poolID)
			return c.ensurePoolUsable(ctx, parent, poolID)
		}
		return "", fmt.Errorf("failed to create Workload Identity Pool: %w", err)
	}

	c.logger.Info("Successfully created Workload Identity Pool", "poolID", poolID)
	return poolID, nil
}

// ensurePoolUsable checks the state of an existing pool and fixes it if needed.
// It handles soft-deleted and disabled pools.
func (c *IAMManager) ensurePoolUsable(ctx context.Context, parent, poolID string) (string, error) {
	poolResource := c.formatPoolResource(parent, poolID)

	// GET to check state
	existingPool, err := c.getWorkloadIdentityPool(ctx, poolResource)
	if err != nil {
		return "", fmt.Errorf("failed to get existing pool: %w", err)
	}

	// Handle DELETED state
	if existingPool.State == "DELETED" {
		c.logger.Info("Pool is soft-deleted, undeleting", "poolID", poolID)
		err = c.undeleteWorkloadIdentityPool(ctx, poolResource)
		if err != nil {
			return "", fmt.Errorf("failed to undelete pool: %w", err)
		}
		c.logger.Info("Successfully undeleted pool", "poolID", poolID)
		// Note: After undelete, pool might still be disabled, so continue to check

		// Re-fetch to get updated state
		existingPool, err = c.getWorkloadIdentityPool(ctx, poolResource)
		if err != nil {
			return "", fmt.Errorf("failed to get pool after undelete: %w", err)
		}
	}

	// Handle Disabled state
	if existingPool.Disabled {
		c.logger.Info("Pool is disabled, enabling", "poolID", poolID)
		existingPool.Disabled = false
		err = c.patchWorkloadIdentityPool(ctx, poolResource, existingPool)
		if err != nil {
			return "", fmt.Errorf("failed to enable pool: %w", err)
		}
		c.logger.Info("Successfully enabled pool", "poolID", poolID)
	}

	c.logger.Info("Pool is usable", "poolID", poolID)
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
			// Provider exists, check and fix its state/config if needed
			c.logger.Info("OIDC Provider already exists, checking state and configuration", "providerID", providerID)
			return c.ensureProviderUsable(ctx, parent, providerID, provider, providerAudience)
		}
		return "", "", fmt.Errorf("failed to create OIDC Provider: %w", err)
	}

	c.logger.Info("Successfully created OIDC Provider", "providerID", providerID, "audience", providerAudience)
	return providerID, providerAudience, nil
}

// ensureProviderUsable checks the state and configuration of an existing provider and fixes it if needed.
// It handles providers that exist but are soft-deleted, disabled, and misconfigured.
func (c *IAMManager) ensureProviderUsable(ctx context.Context, parent, providerID string, expectedProvider *iam.WorkloadIdentityPoolProvider, providerAudience string) (string, string, error) {
	providerResource := fmt.Sprintf("%s/providers/%s", parent, providerID)

	// GET to check state and configuration
	existingProvider, err := c.getWorkloadIdentityProvider(ctx, providerResource)
	if err != nil {
		return "", "", fmt.Errorf("failed to get existing provider: %w", err)
	}

	// Handle DELETED state first - must undelete before we can update
	if existingProvider.State == "DELETED" {
		c.logger.Info("Provider is soft-deleted, undeleting", "providerID", providerID)
		err = c.undeleteWorkloadIdentityProvider(ctx, providerResource)
		if err != nil {
			return "", "", fmt.Errorf("failed to undelete provider: %w", err)
		}
		c.logger.Info("Successfully undeleted provider", "providerID", providerID)

		// Re-fetch to get updated state after undelete
		existingProvider, err = c.getWorkloadIdentityProvider(ctx, providerResource)
		if err != nil {
			return "", "", fmt.Errorf("failed to get provider after undelete: %w", err)
		}
	}

	// Check if provider needs updates (config drift or disabled state)
	disabledMismatch := existingProvider.Disabled

	// Guard against nil OIDC config - treat missing config as a mismatch
	var issuerMismatch, jwksMismatch bool
	if existingProvider.Oidc == nil || expectedProvider.Oidc == nil {
		// Nil OIDC config should be treated as a mismatch requiring update
		c.logger.Info("Provider has nil OIDC config, treating as mismatch",
			"providerID", providerID,
			"existingOidcNil", existingProvider.Oidc == nil,
			"expectedOidcNil", expectedProvider.Oidc == nil)
		issuerMismatch = true
		jwksMismatch = true
	} else {
		issuerMismatch = existingProvider.Oidc.IssuerUri != expectedProvider.Oidc.IssuerUri
		// Compare JWKS semantically (parse and compare structure, not string)
		jwksMismatch = !c.compareJWKS(existingProvider.Oidc.JwksJson, expectedProvider.Oidc.JwksJson)
	}

	needsUpdate := disabledMismatch || issuerMismatch || jwksMismatch

	if needsUpdate {
		c.logger.Info("Provider needs updates",
			"providerID", providerID,
			"disabledMismatch", disabledMismatch,
			"issuerMismatch", issuerMismatch,
			"jwksMismatch", jwksMismatch)

		// Update the provider configuration
		expectedProvider.Name = providerResource
		expectedProvider.Disabled = false // Ensure provider is enabled
		err = c.updateWorkloadIdentityProvider(ctx, providerResource, expectedProvider)
		if err != nil {
			return "", "", fmt.Errorf("failed to update provider: %w", err)
		}
		c.logger.Info("Successfully updated provider", "providerID", providerID)
	} else {
		c.logger.Info("Provider is already usable with correct configuration", "providerID", providerID)
	}

	return providerID, providerAudience, nil
}

// CreateServiceAccounts creates all Google Service Accounts defined in the template,
// assigns their roles, and creates WIF bindings.
func (c *IAMManager) CreateServiceAccounts(ctx context.Context) (map[string]string, error) {
	serviceAccountEmails := make(map[string]string)

	definitions, err := loadServiceAccountDefinitions("")
	if err != nil {
		return nil, fmt.Errorf("failed to load service account definitions: %w", err)
	}

	for _, def := range definitions {
		c.logger.Info("Processing service account", "name", def.Name)

		// Create the GSA
		email, err := c.createServiceAccount(ctx, def)
		if err != nil {
			return nil, fmt.Errorf("failed to create service account %s: %w", def.Name, err)
		}
		serviceAccountEmails[def.Name] = email

		// Assign roles to the GSA (with retry for IAM propagation)
		if len(def.Roles) > 0 {
			err := c.retryWithExponentialBackoff(ctx, fmt.Sprintf("assignRoles-%s", def.Name), func() error {
				return c.assignRoles(ctx, email, def.Roles)
			})
			if err != nil {
				return nil, fmt.Errorf("failed to assign roles to %s: %w", def.Name, err)
			}
		}

		// Create WIF binding if K8s SA is specified (with retry for IAM propagation)
		if def.K8sServiceAccount != nil {
			err := c.retryWithExponentialBackoff(ctx, fmt.Sprintf("createWIFBinding-%s", def.Name), func() error {
				return c.createWorkloadIdentityBinding(ctx, email, def.K8sServiceAccount)
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create WIF binding for %s: %w", def.Name, err)
			}
		}

		c.logger.Info("Successfully configured service account", "name", def.Name, "email", email)
	}

	return serviceAccountEmails, nil
}

// createServiceAccount creates a single Google Service Account.
func (c *IAMManager) createServiceAccount(ctx context.Context, def ServiceAccountDefinition) (string, error) {
	accountID := c.formatServiceAccountID(def.Name)
	email := c.formatServiceAccountEmail(def.Name)

	c.logger.Info("Creating service account", "accountID", accountID, "displayName", def.DisplayName)

	serviceAccount := &iam.ServiceAccount{
		DisplayName: def.DisplayName,
		Description: def.Description,
	}

	_, err := c.createGoogleServiceAccount(ctx, accountID, serviceAccount)
	if err != nil {
		if isAlreadyExistsError(err) {
			c.logger.Info("Service account already exists", "email", email)
			return email, nil
		}
		return "", err
	}

	c.logger.Info("Successfully created service account", "email", email)
	return email, nil
}

// assignRoles assigns IAM roles to a Google Service Account at the project level.
func (c *IAMManager) assignRoles(ctx context.Context, serviceAccountEmail string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}

	member := fmt.Sprintf("serviceAccount:%s", serviceAccountEmail)

	// Get current IAM policy once
	policy, err := c.getProjectIAMPolicy(ctx)
	if err != nil {
		return fmt.Errorf("failed to get IAM policy: %w", err)
	}

	policyModified := false

	for _, role := range roles {
		c.logger.Info("Checking role binding", "role", role, "member", member)

		memberAdded := c.addMemberToRoleBinding(policy, role, member)
		if memberAdded {
			policyModified = true
			c.logger.Info("Added role binding", "role", role, "member", member)
		} else {
			c.logger.Info("Role binding already exists", "role", role, "member", member)
		}
	}

	// Only update policy if changes were made
	if policyModified {
		if err := c.setProjectIAMPolicy(ctx, policy); err != nil {
			return fmt.Errorf("failed to set IAM policy: %w", err)
		}
		c.logger.Info("Updated project IAM policy", "member", member, "rolesModified", len(roles))
	} else {
		c.logger.Info("No policy changes needed", "member", member)
	}

	return nil
}

// addMemberToRoleBinding adds a member to a role binding in the policy if not already present.
// Returns true if the member was added, false if it already existed.
func (c *IAMManager) addMemberToRoleBinding(policy *cloudresourcemanager.Policy, role, member string) bool {
	// Check if role binding exists and if member is already in it
	for _, binding := range policy.Bindings {
		if binding.Role == role {
			// Check if member already exists
			for _, existingMember := range binding.Members {
				if existingMember == member {
					return false // Member already exists
				}
			}
			// Member doesn't exist, add it
			binding.Members = append(binding.Members, member)
			return true
		}
	}

	// Role binding doesn't exist, create it
	policy.Bindings = append(policy.Bindings, &cloudresourcemanager.Binding{
		Role:    role,
		Members: []string{member},
	})
	return true
}

// createWorkloadIdentityBinding creates a WIF binding between a Kubernetes SA and a Google SA.
func (c *IAMManager) createWorkloadIdentityBinding(ctx context.Context, serviceAccountEmail string, k8sSA *K8sServiceAccountRef) error {
	// The binding is created by granting the K8s SA permission to impersonate the GSA
	member := c.formatWIFPrincipal(k8sSA.Namespace, k8sSA.Name)

	k8sSAName := fmt.Sprintf("%s/%s", k8sSA.Namespace, k8sSA.Name)
	c.logger.Info("Checking WIF binding", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)

	// Get current IAM policy for the service account
	policy, err := c.getServiceAccountIAMPolicy(ctx, serviceAccountEmail)
	if err != nil {
		return fmt.Errorf("failed to get service account IAM policy: %w", err)
	}

	// Add the binding for workloadIdentityUser role
	memberAdded := c.addMemberToServiceAccountRoleBinding(policy, workloadIdentityUserRole, member)

	// Only update policy if changes were made
	if memberAdded {
		if err := c.setServiceAccountIAMPolicy(ctx, serviceAccountEmail, policy); err != nil {
			return fmt.Errorf("failed to set IAM policy for WIF binding: %w", err)
		}
		c.logger.Info("Created WIF binding", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)
	} else {
		c.logger.Info("WIF binding already exists", "k8sSA", k8sSAName, "gsaEmail", serviceAccountEmail)
	}

	return nil
}

// addMemberToServiceAccountRoleBinding adds a member to a role binding in the service account policy if not already present.
// Returns true if the member was added, false if it already existed.
func (c *IAMManager) addMemberToServiceAccountRoleBinding(policy *iam.Policy, role, member string) bool {

	// Check if role binding exists and if member is already in it
	for _, binding := range policy.Bindings {
		if binding.Role == role {
			// Check if member already exists
			for _, existingMember := range binding.Members {
				if existingMember == member {
					return false // Member already exists
				}
			}
			// Member doesn't exist, add it
			binding.Members = append(binding.Members, member)
			return true
		}
	}

	// Role binding doesn't exist, create it
	policy.Bindings = append(policy.Bindings, &iam.Binding{
		Role:    role,
		Members: []string{member},
	})
	return true
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

// formatServiceAccountID returns the service account ID for a given component name.
func (c *IAMManager) formatServiceAccountID(componentName string) string {
	return fmt.Sprintf("%s-%s", c.infraID, componentName)
}

// formatServiceAccountEmail returns the full email address for a service account.
func (c *IAMManager) formatServiceAccountEmail(componentName string) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", c.formatServiceAccountID(componentName), c.projectID)
}

// formatServiceAccountResource returns the full resource path for a service account.
func (c *IAMManager) formatServiceAccountResource(serviceAccountEmail string) string {
	return fmt.Sprintf("projects/%s/serviceAccounts/%s", c.projectID, serviceAccountEmail)
}

// formatProjectResource returns the full resource path for the project.
func (c *IAMManager) formatProjectResource() string {
	return fmt.Sprintf("projects/%s", c.projectID)
}

// formatWIFPrincipal returns the WIF principal member format for a Kubernetes service account.
func (c *IAMManager) formatWIFPrincipal(namespace, serviceAccountName string) string {
	return fmt.Sprintf("principal://iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/subject/system:serviceaccount:%s:%s",
		c.projectNumber, c.formatPoolID(), namespace, serviceAccountName)
}

// formatPoolResource returns the full resource path for a Workload Identity Pool.
func (c *IAMManager) formatPoolResource(parent, poolID string) string {
	return fmt.Sprintf("%s/workloadIdentityPools/%s", parent, poolID)
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

// waitOperation polls a long-running operation until completion or timeout.
func (c *IAMManager) waitOperation(ctx context.Context, opName string) error {
	deadline := time.Now().Add(defaultOperationTimeout)
	for {
		// Check if context is canceled or deadline exceeded
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("operation canceled: %w", err)
		}
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

// compareJWKS performs a semantic comparison of two JWKS JSON strings.
// It parses both as JSON and compares the resulting structures, ignoring
// differences in whitespace, key ordering, or formatting.
func (c *IAMManager) compareJWKS(jwks1, jwks2 string) bool {
	var obj1, obj2 map[string]any

	if err := json.Unmarshal([]byte(jwks1), &obj1); err != nil {
		c.logger.Info("Failed to parse existing JWKS JSON", "error", err)
		return false
	}

	if err := json.Unmarshal([]byte(jwks2), &obj2); err != nil {
		c.logger.Info("Failed to parse expected JWKS JSON", "error", err)
		return false
	}

	// Marshal both to a canonical form and compare
	canonical1, err1 := json.Marshal(obj1)
	canonical2, err2 := json.Marshal(obj2)

	if err1 != nil || err2 != nil {
		c.logger.Info("Failed to marshal JWKS for comparison", "err1", err1, "err2", err2)
		return false
	}

	return string(canonical1) == string(canonical2)
}

// isTransientIAMError checks if the error is likely due to IAM eventual consistency.
// These errors should be retried as they typically resolve once IAM changes propagate.
func isTransientIAMError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 404:
			// Not found - resource may not have propagated yet
			return true
		case 400:
			// Bad request - sometimes occurs during IAM propagation
			// Check if it's related to IAM/permissions or resource propagation
			return strings.Contains(apiErr.Message, "IAM") ||
				strings.Contains(apiErr.Message, "permission") ||
				strings.Contains(apiErr.Message, "policy") ||
				strings.Contains(apiErr.Message, "does not exist") ||
				strings.Contains(apiErr.Message, "Service account")
		case 403:
			// Permission denied - might be temporary during propagation
			return strings.Contains(apiErr.Message, "Permission") ||
				strings.Contains(apiErr.Message, "policy")
		}
	}

	return false
}

// retryWithExponentialBackoff retries an operation with exponential backoff.
// It only retries on transient IAM errors and respects the context deadline.
func (c *IAMManager) retryWithExponentialBackoff(ctx context.Context, operationName string, operation func() error) error {
	deadline := time.Now().Add(iamPropagationTimeout)
	backoff := iamPropagationInitialBackoff
	attempt := 0

	for {
		attempt++
		err := operation()

		if err == nil {
			if attempt > 1 {
				c.logger.Info("Operation succeeded after retry", "operation", operationName, "attempts", attempt)
			}
			return nil
		}

		// Check if error is transient
		if !isTransientIAMError(err) {
			c.logger.Info("Operation failed with non-transient error", "operation", operationName, "error", err)
			return err
		}

		// Check if we've exceeded the deadline
		if time.Now().After(deadline) {
			c.logger.Info("Operation timed out after retries", "operation", operationName, "attempts", attempt, "lastError", err)
			return fmt.Errorf("operation timed out after %d attempts due to IAM propagation delays: %w", attempt, err)
		}

		// Check context cancellation
		if ctx.Err() != nil {
			return fmt.Errorf("operation canceled: %w", ctx.Err())
		}

		// Add jitter to backoff to avoid thundering herd (±25% randomization)
		jitter := time.Duration(float64(backoff) * (0.75 + rand.Float64()*0.5))

		c.logger.Info("Retrying operation due to IAM propagation delay",
			"operation", operationName,
			"attempt", attempt,
			"backoff", jitter,
			"error", err.Error())

		// Wait before retry with jitter
		select {
		case <-time.After(jitter):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("operation canceled during backoff: %w", ctx.Err())
		}

		// Increase backoff exponentially, capped at max
		backoff *= 2
		if backoff > iamPropagationMaxBackoff {
			backoff = iamPropagationMaxBackoff
		}
	}
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

// createGoogleServiceAccount calls the GCP IAM API to create a service account.
func (c *IAMManager) createGoogleServiceAccount(ctx context.Context, accountID string, serviceAccount *iam.ServiceAccount) (*iam.ServiceAccount, error) {
	name := c.formatProjectResource()
	request := &iam.CreateServiceAccountRequest{
		AccountId:      accountID,
		ServiceAccount: serviceAccount,
	}

	return c.iamService.Projects.ServiceAccounts.Create(name, request).Context(ctx).Do()
}

// getProjectIAMPolicy calls the GCP CRM API to get the project's IAM policy.
func (c *IAMManager) getProjectIAMPolicy(ctx context.Context) (*cloudresourcemanager.Policy, error) {
	// CRM API expects just the project ID, not "projects/{projectID}"
	return c.crmService.Projects.GetIamPolicy(c.projectID, &cloudresourcemanager.GetIamPolicyRequest{}).Context(ctx).Do()
}

// setProjectIAMPolicy calls the GCP CRM API to set the project's IAM policy.
func (c *IAMManager) setProjectIAMPolicy(ctx context.Context, policy *cloudresourcemanager.Policy) error {
	// CRM API expects just the project ID, not "projects/{projectID}"
	setRequest := &cloudresourcemanager.SetIamPolicyRequest{
		Policy: policy,
	}
	_, err := c.crmService.Projects.SetIamPolicy(c.projectID, setRequest).Context(ctx).Do()
	return err
}

// getServiceAccountIAMPolicy calls the GCP IAM API to get a service account's IAM policy.
func (c *IAMManager) getServiceAccountIAMPolicy(ctx context.Context, serviceAccountEmail string) (*iam.Policy, error) {
	resource := c.formatServiceAccountResource(serviceAccountEmail)
	return c.iamService.Projects.ServiceAccounts.GetIamPolicy(resource).Context(ctx).Do()
}

// setServiceAccountIAMPolicy calls the GCP IAM API to set a service account's IAM policy.
func (c *IAMManager) setServiceAccountIAMPolicy(ctx context.Context, serviceAccountEmail string, policy *iam.Policy) error {
	resource := c.formatServiceAccountResource(serviceAccountEmail)
	setRequest := &iam.SetIamPolicyRequest{
		Policy: policy,
	}
	_, err := c.iamService.Projects.ServiceAccounts.SetIamPolicy(resource, setRequest).Context(ctx).Do()
	return err
}

// getWorkloadIdentityPool calls the GCP IAM API to get a Workload Identity Pool.
func (c *IAMManager) getWorkloadIdentityPool(ctx context.Context, poolResource string) (*iam.WorkloadIdentityPool, error) {
	return c.iamService.Projects.Locations.WorkloadIdentityPools.Get(poolResource).Context(ctx).Do()
}

// undeleteWorkloadIdentityPool calls the GCP IAM API to undelete a Workload Identity Pool.
func (c *IAMManager) undeleteWorkloadIdentityPool(ctx context.Context, poolResource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Undelete(
		poolResource,
		&iam.UndeleteWorkloadIdentityPoolRequest{},
	).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// patchWorkloadIdentityPool calls the GCP IAM API to patch a Workload Identity Pool.
func (c *IAMManager) patchWorkloadIdentityPool(ctx context.Context, poolResource string, pool *iam.WorkloadIdentityPool) error {
	// Specify which fields to update - currently only updating disabled state
	updateMask := "disabled"
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.
		Patch(poolResource, pool).
		UpdateMask(updateMask).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// getWorkloadIdentityProvider calls the GCP IAM API to get a Workload Identity Provider.
func (c *IAMManager) getWorkloadIdentityProvider(ctx context.Context, providerResource string) (*iam.WorkloadIdentityPoolProvider, error) {
	return c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Get(providerResource).Context(ctx).Do()
}

// updateWorkloadIdentityProvider calls the GCP IAM API to update a Workload Identity Provider.
func (c *IAMManager) updateWorkloadIdentityProvider(ctx context.Context, providerResource string, provider *iam.WorkloadIdentityPoolProvider) error {
	// Specify which fields to update - we may be updating disabled state, JWKS, or issuer URI
	updateMask := "disabled,oidc.jwks_json,oidc.issuer_uri"
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.
		Patch(providerResource, provider).
		UpdateMask(updateMask).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}

// undeleteWorkloadIdentityProvider calls the GCP IAM API to undelete a Workload Identity Provider.
func (c *IAMManager) undeleteWorkloadIdentityProvider(ctx context.Context, providerResource string) error {
	op, err := c.iamService.Projects.Locations.WorkloadIdentityPools.Providers.Undelete(
		providerResource,
		&iam.UndeleteWorkloadIdentityPoolProviderRequest{},
	).Context(ctx).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(ctx, op.Name)
}
