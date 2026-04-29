package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	azureauth "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
)

const (
	graphAPIEndpoint = "https://graph.microsoft.com/v1.0/servicePrincipals"
)

// roleAssignmentClient abstracts the Azure role assignment operations for testability.
type roleAssignmentClient interface {
	Get(ctx context.Context, scope string, roleAssignmentName string, options *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error)
	Delete(ctx context.Context, scope string, roleAssignmentName string, options *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error)
	Create(ctx context.Context, scope string, roleAssignmentName string, parameters azureauth.RoleAssignmentCreateParameters, options *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error)
	NewListForScopePager(scope string, options *azureauth.RoleAssignmentsClientListForScopeOptions) *runtime.Pager[azureauth.RoleAssignmentsClientListForScopeResponse]
}

// RBACManager handles Azure RBAC operations
type RBACManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
}

// ServicePrincipalResponse represents the response from Microsoft Graph API
type ServicePrincipalResponse struct {
	Value []ServicePrincipal `json:"value"`
}

// ServicePrincipal represents a service principal from Microsoft Graph API
type ServicePrincipal struct {
	ID string `json:"id"`
}

// NewRBACManager creates a new RBACManager
func NewRBACManager(subscriptionID string, creds azcore.TokenCredential) *RBACManager {
	return &RBACManager{
		subscriptionID: subscriptionID,
		creds:          creds,
	}
}

// AssignControlPlaneRoles assigns roles to control plane managed identities
func (r *RBACManager) AssignControlPlaneRoles(ctx context.Context, opts *CreateInfraOptions, controlPlaneMIs *hyperv1.AzureResourceManagedIdentities, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName string) error {
	components := map[string]hyperv1.AzureClientID{
		config.CPO:           controlPlaneMIs.ControlPlane.ControlPlaneOperator.ClientID,
		config.NodePoolMgmt:  controlPlaneMIs.ControlPlane.NodePoolManagement.ClientID,
		config.CloudProvider: controlPlaneMIs.ControlPlane.CloudProvider.ClientID,
		config.AzureFile:     controlPlaneMIs.ControlPlane.File.ClientID,
		config.AzureDisk:     controlPlaneMIs.ControlPlane.Disk.ClientID,
		config.Ingress:       controlPlaneMIs.ControlPlane.Ingress.ClientID,
		config.CNCC:          controlPlaneMIs.ControlPlane.Network.ClientID,
	}

	if !slices.Contains(opts.DisableClusterCapabilities, string(hyperv1.ImageRegistryCapability)) {
		components[config.CIRO] = controlPlaneMIs.ControlPlane.ImageRegistry.ClientID
	}

	return r.assignRolesForComponents(ctx, opts, components, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName)
}

// AssignWorkloadIdentities assigns roles to workload identity managed identities
func (r *RBACManager) AssignWorkloadIdentities(ctx context.Context, opts *CreateInfraOptions, workloadIdentities *hyperv1.AzureWorkloadIdentities, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName string) error {
	components := map[string]hyperv1.AzureClientID{
		config.NodePoolMgmt:  workloadIdentities.NodePoolManagement.ClientID,
		config.CloudProvider: workloadIdentities.CloudProvider.ClientID,
		config.AzureFile:     workloadIdentities.File.ClientID,
		config.AzureDisk:     workloadIdentities.Disk.ClientID,
		config.Ingress:       workloadIdentities.Ingress.ClientID,
		config.CNCC:          workloadIdentities.Network.ClientID,
	}

	if !slices.Contains(opts.DisableClusterCapabilities, string(hyperv1.ImageRegistryCapability)) {
		components[config.CIRO] = workloadIdentities.ImageRegistry.ClientID
	}

	if workloadIdentities.ControlPlaneOperator.ClientID != "" {
		components[config.CPO] = workloadIdentities.ControlPlaneOperator.ClientID
	}

	return r.assignRolesForComponents(ctx, opts, components, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName)
}

// assignRolesForComponents resolves object IDs and assigns scoped roles for each component.
func (r *RBACManager) assignRolesForComponents(ctx context.Context, opts *CreateInfraOptions, components map[string]hyperv1.AzureClientID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName string) error {
	token, err := r.getAzureToken()
	if err != nil {
		return err
	}

	raClient, err := azureauth.NewRoleAssignmentsClient(r.subscriptionID, r.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	for component, clientID := range components {
		objectID, err := r.getObjectIDFromClientID(string(clientID), token)
		if err != nil {
			return err
		}

		role, scopes := azureutil.GetServicePrincipalScopes(r.subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, opts.DNSZoneRG, component, opts.AssignCustomHCPRoles)

		for _, scope := range scopes {
			if err := r.assignRole(ctx, raClient, opts.InfraID, component, objectID, role, scope); err != nil {
				return fmt.Errorf("failed to perform role assignment: %w", err)
			}
		}
	}

	return nil
}

// AssignDataPlaneRoles assigns roles to data plane managed identities
func (r *RBACManager) AssignDataPlaneRoles(ctx context.Context, opts *CreateInfraOptions, dataPlaneIdentities hyperv1.DataPlaneManagedIdentities, resourceGroupName string) error {
	managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", r.subscriptionID, resourceGroupName)

	// Get an access token for Microsoft Graph API for getting the object IDs
	token, err := r.getAzureToken()
	if err != nil {
		return err
	}

	raClient, err := azureauth.NewRoleAssignmentsClient(r.subscriptionID, r.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	// Setup Data Plane MI role assignments
	objectID, err := r.getObjectIDFromClientID(dataPlaneIdentities.ImageRegistryMSIClientID, token)
	if err != nil {
		return err
	}
	err = r.assignRole(ctx, raClient, opts.InfraID, config.CIRO+"WI", objectID, config.ImageRegistryRoleDefinitionID, managedRG)
	if err != nil {
		return err
	}

	objectID, err = r.getObjectIDFromClientID(dataPlaneIdentities.DiskMSIClientID, token)
	if err != nil {
		return err
	}
	err = r.assignRole(ctx, raClient, opts.InfraID, config.AzureDisk+"WI", objectID, config.AzureDiskRoleDefinitionID, managedRG)
	if err != nil {
		return err
	}

	objectID, err = r.getObjectIDFromClientID(dataPlaneIdentities.FileMSIClientID, token)
	if err != nil {
		return err
	}
	err = r.assignRole(ctx, raClient, opts.InfraID, config.AzureFile+"WI", objectID, config.AzureFileRoleDefinitionID, managedRG)
	if err != nil {
		return err
	}

	return nil
}

// assignRole assigns a scoped role to the service principal assignee
func (r *RBACManager) assignRole(ctx context.Context, client roleAssignmentClient, infraID, component, assigneeID, role, scope string) error {
	// Generate the role assignment name
	roleAssignmentName := util.GenerateRoleAssignmentName(infraID, component, scope)

	// Generate the role definition ID
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", r.subscriptionID, role)

	// Generate the role assignment properties
	roleAssignmentProperties := azureauth.RoleAssignmentCreateParameters{
		Properties: &azureauth.RoleAssignmentProperties{
			PrincipalID:      ptr.To(assigneeID),
			RoleDefinitionID: ptr.To(roleDefinitionID),
			Scope:            ptr.To(scope),
		},
	}

	// Robust existence check:
	// 1) List assignments for this principalId at or around this scope and
	//    verify one matches both the exact scope and role definition ID.
	pager := client.NewListForScopePager(scope, &azureauth.RoleAssignmentsClientListForScopeOptions{
		// Use atScope() to reliably list assignments at this scope, then match in code
		Filter: ptr.To("atScope()"),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments for scope: %w", err)
		}
		for _, ra := range page.Value {
			if ra.Properties == nil {
				continue
			}
			if ra.Properties.RoleDefinitionID == nil || ra.Properties.Scope == nil || ra.Properties.PrincipalID == nil {
				continue
			}
			if strings.EqualFold(*ra.Properties.Scope, scope) && strings.EqualFold(*ra.Properties.RoleDefinitionID, roleDefinitionID) && strings.EqualFold(*ra.Properties.PrincipalID, assigneeID) {
				log.Log.Info("Skipping role assignment creation, matching assignment already exists.", "role", role, "assigneeID", assigneeID, "scope", scope)
				return nil
			}
		}
	}

	// 2) Fallback to a direct GET by our deterministic name; create only if 404.
	//    If the assignment exists but points to a different principal (stale/orphaned from a
	//    previous cluster with the same infraID), delete it and fall through to create a new one.
	existing, err := client.Get(ctx, scope, roleAssignmentName, nil)
	if err == nil {
		if existing.Properties != nil &&
			existing.Properties.PrincipalID != nil &&
			existing.Properties.RoleDefinitionID != nil &&
			strings.EqualFold(*existing.Properties.PrincipalID, assigneeID) &&
			strings.EqualFold(*existing.Properties.RoleDefinitionID, roleDefinitionID) {
			log.Log.Info("Skipping role assignment creation, role assignment already exists.", "role", role, "assigneeID", assigneeID, "scope", scope)
			return nil
		}
		// Stale assignment — different principal owns this deterministic name.
		stalePrincipal := "<nil>"
		if existing.Properties != nil && existing.Properties.PrincipalID != nil {
			stalePrincipal = *existing.Properties.PrincipalID
		}
		log.Log.Info("Deleting stale role assignment with mismatched principal",
			"role", role, "expectedPrincipal", assigneeID,
			"stalePrincipal", stalePrincipal,
			"scope", scope)
		if _, err := client.Delete(ctx, scope, roleAssignmentName, nil); err != nil {
			return fmt.Errorf("failed to delete stale role assignment: %w", err)
		}
		// Fall through to create a fresh assignment below.
	} else {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			if respErr.StatusCode == http.StatusNotFound {
				// proceed to create
			} else if respErr.StatusCode == http.StatusForbidden || strings.EqualFold(respErr.ErrorCode, "AuthorizationFailed") {
				log.Log.Info("Get not permitted; will attempt create and rely on 409 for idempotency.", "role", role, "assigneeID", assigneeID, "scope", scope)
			} else {
				return fmt.Errorf("failed checking role assignment existence: %w", err)
			}
		} else {
			return fmt.Errorf("failed to check role assignment existence: %w", err)
		}
	}

	_, err = client.Create(ctx, scope, roleAssignmentName, roleAssignmentProperties, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && (respErr.StatusCode == http.StatusConflict || strings.EqualFold(respErr.ErrorCode, "RoleAssignmentExists")) {
			log.Log.Info("Failed role assignment creation, role assignment already exists.", "role", role, "assigneeID", assigneeID, "scope", scope)
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %w", err)
	}
	log.Log.Info("successfully created role assignment", "role", role, "assigneeID", assigneeID, "scope", scope)
	return nil
}

// CleanupRoleAssignments deletes all role assignments created for a cluster's workload identities.
// It regenerates the deterministic role assignment names from the infraID, component names, and scopes,
// then deletes each one. This must be called before destroying managed identities to avoid orphaned
// role assignments that cause naming collisions on re-creation.
func (r *RBACManager) CleanupRoleAssignments(ctx context.Context, l logr.Logger, infraID string, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneRG string, assignCustomHCPRoles bool) error {
	raClient, err := azureauth.NewRoleAssignmentsClient(r.subscriptionID, r.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}
	return r.cleanupRoleAssignments(ctx, l, raClient, infraID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneRG, assignCustomHCPRoles)
}

// cleanupRoleAssignments is the testable inner method that performs the actual cleanup.
func (r *RBACManager) cleanupRoleAssignments(ctx context.Context, l logr.Logger, client roleAssignmentClient, infraID string, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneRG string, assignCustomHCPRoles bool) error {
	// All components that may have role assignments
	components := []string{
		config.CPO,
		config.NodePoolMgmt,
		config.CloudProvider,
		config.AzureFile,
		config.AzureDisk,
		config.Ingress,
		config.CNCC,
		config.CIRO,
	}

	// Data plane components use a "WI" suffix
	dataPlaneComponents := []string{
		config.CIRO + "WI",
		config.AzureDisk + "WI",
		config.AzureFile + "WI",
	}

	var deleteErrors []error

	// Cleanup control plane and workload identity role assignments
	for _, component := range components {
		_, scopes := azureutil.GetServicePrincipalScopes(r.subscriptionID, resourceGroupName, nsgResourceGroupName, vnetResourceGroupName, dnsZoneRG, component, assignCustomHCPRoles)
		for _, scope := range scopes {
			name := util.GenerateRoleAssignmentName(infraID, component, scope)
			if err := r.deleteRoleAssignmentByName(ctx, l, client, scope, name, component); err != nil {
				deleteErrors = append(deleteErrors, err)
			}
		}
	}

	// Cleanup data plane role assignments (only scoped to managed RG)
	managedRG := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", r.subscriptionID, resourceGroupName)
	for _, component := range dataPlaneComponents {
		name := util.GenerateRoleAssignmentName(infraID, component, managedRG)
		if err := r.deleteRoleAssignmentByName(ctx, l, client, managedRG, name, component); err != nil {
			deleteErrors = append(deleteErrors, err)
		}
	}

	if len(deleteErrors) > 0 {
		return fmt.Errorf("failed to delete %d role assignments during cleanup: %w", len(deleteErrors), errors.Join(deleteErrors...))
	}

	l.Info("Successfully cleaned up all role assignments", "infraID", infraID)
	return nil
}

func (r *RBACManager) deleteRoleAssignmentByName(ctx context.Context, l logr.Logger, client roleAssignmentClient, scope, name, component string) error {
	_, err := client.Delete(ctx, scope, name, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			l.Info("Role assignment not found, skipping", "component", component, "scope", scope)
			return nil
		}
		l.Error(err, "Failed to delete role assignment", "component", component, "scope", scope)
		return err
	}
	l.Info("Deleted role assignment", "component", component, "scope", scope)
	return nil
}

func (r *RBACManager) getAzureToken() (azcore.AccessToken, error) {
	token, err := r.creds.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("failed to get access token: %w", err)
	}

	return token, nil
}

func (r *RBACManager) getObjectIDFromClientID(clientID string, token azcore.AccessToken) (string, error) {
	// Validate clientID is a UUID to prevent OData injection
	uuidPattern := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	if !uuidPattern.MatchString(clientID) {
		return "", fmt.Errorf("invalid client ID format: must be a UUID")
	}

	filterQuery := "$filter=appId eq '" + clientID + "'"
	url := graphAPIEndpoint + "?" + strings.ReplaceAll(filterQuery, " ", "%20")

	// Make the API request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set Authorization header
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("graph API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result ServicePrincipalResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Value) == 0 {
		return "", fmt.Errorf("no object id found for client id: %s", clientID)
	}

	if len(result.Value) > 1 {
		return "", fmt.Errorf("more than one object id found for client id: %s", clientID)
	}

	return result.Value[0].ID, nil
}
