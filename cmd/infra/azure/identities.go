package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
)

// IdentityManager handles Azure managed identity and federated credential operations
type IdentityManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
	cloud          string
}

// NewIdentityManager creates a new IdentityManager
func NewIdentityManager(subscriptionID string, creds azcore.TokenCredential, cloud string) *IdentityManager {
	return &IdentityManager{
		subscriptionID: subscriptionID,
		creds:          creds,
		cloud:          cloud,
	}
}

// deleteFederatedIdentityCredential deletes a federated identity credential from a managed identity.
func (i *IdentityManager) deleteFederatedIdentityCredential(ctx context.Context, l logr.Logger, resourceGroupName string, identityName string, credentialName string) error {
	l.Info("Deleting federated identity credential",
		"credentialName", credentialName,
		"identityName", identityName,
		"resourceGroup", resourceGroupName)

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(i.cloud)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

	client, err := armmsi.NewFederatedIdentityCredentialsClient(i.subscriptionID, i.creds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create federated identity credentials client: %w", err)
	}

	_, err = client.Delete(ctx, resourceGroupName, identityName, credentialName, nil)
	if err != nil {
		if isNotFoundError(err) {
			l.Info("Federated identity credential not found, skipping deletion",
				"credentialName", credentialName,
				"identityName", identityName)
			return nil
		}
		return fmt.Errorf("failed to delete federated credential '%s': %w", credentialName, err)
	}

	l.Info("Successfully deleted federated identity credential",
		"credentialName", credentialName,
		"identityName", identityName)

	return nil
}

// deleteManagedIdentity deletes a managed identity using Azure SDK.
func (i *IdentityManager) deleteManagedIdentity(ctx context.Context, l logr.Logger, resourceGroupName string, identityName string) error {
	l.Info("Deleting managed identity",
		"name", identityName,
		"resourceGroup", resourceGroupName,
		"subscription", i.subscriptionID)

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(i.cloud)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

	client, err := armmsi.NewUserAssignedIdentitiesClient(i.subscriptionID, i.creds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create managed identity client: %w", err)
	}

	_, err = client.Delete(ctx, resourceGroupName, identityName, nil)
	if err != nil {
		// Check if the error indicates the identity doesn't exist
		if isNotFoundError(err) {
			l.Info("Managed identity not found, skipping deletion",
				"name", identityName,
				"resourceGroup", resourceGroupName)
			return nil
		}
		return fmt.Errorf("failed to delete managed identity '%s': %w", identityName, err)
	}

	l.Info("Successfully deleted managed identity",
		"name", identityName,
		"resourceGroup", resourceGroupName)

	return nil
}

// isNotFoundError checks if the error indicates a resource was not found
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}

// createManagedIdentity creates a managed identity using Azure SDK
// Returns: resourceID, clientID, principalID, error
func (i *IdentityManager) createManagedIdentity(ctx context.Context, l logr.Logger, resourceGroupName string, name string, infraID string, location string) (string, string, string, error) {
	identityName := fmt.Sprintf("%s-%s", name, infraID)

	l.Info("Creating managed identity",
		"name", identityName,
		"resourceGroup", resourceGroupName,
		"location", location,
		"subscription", i.subscriptionID)

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(i.cloud)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

	client, err := armmsi.NewUserAssignedIdentitiesClient(i.subscriptionID, i.creds, clientOptions)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create managed identity client: %w", err)
	}

	identity, err := client.CreateOrUpdate(ctx, resourceGroupName, identityName, armmsi.Identity{
		Location: ptr.To(location),
	}, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create managed identity: %w", err)
	}

	if identity.ID == nil || identity.Properties == nil || identity.Properties.ClientID == nil || identity.Properties.PrincipalID == nil {
		return "", "", "", fmt.Errorf("managed identity response missing required fields")
	}

	l.Info("Successfully created managed identity",
		"name", identityName,
		"resourceID", ptr.Deref(identity.ID, ""),
		"clientID", ptr.Deref(identity.Properties.ClientID, ""),
		"principalID", ptr.Deref(identity.Properties.PrincipalID, ""),
		"resourceGroup", resourceGroupName)

	return ptr.Deref(identity.ID, ""), ptr.Deref(identity.Properties.ClientID, ""), ptr.Deref(identity.Properties.PrincipalID, ""), nil
}

// FederatedCredentialConfig holds configuration for creating federated identity credentials
type FederatedCredentialConfig struct {
	CredentialName string
	Subject        string
	Audience       string
}

// WorkloadIdentityDefinition defines a workload identity component with its federated credentials
type WorkloadIdentityDefinition struct {
	ComponentName        string // e.g., "disk", "file", "ingress"
	IdentityNameSuffix   string // e.g., "-disk", "-file"
	FederatedCredentials []FederatedCredentialConfig
}

// GetWorkloadIdentityDefinitions returns all workload identity definitions for a cluster.
// This is the single source of truth for identity names and their federated credentials,
// used by both create and destroy operations.
func GetWorkloadIdentityDefinitions(clusterName string) []WorkloadIdentityDefinition {
	return []WorkloadIdentityDefinition{
		{
			ComponentName:      "disk",
			IdentityNameSuffix: "-disk",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-disk-fed-id-node",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
					Audience:       "openshift",
				},
				{
					CredentialName: clusterName + "-disk-fed-id-operator",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator",
					Audience:       "openshift",
				},
				{
					CredentialName: clusterName + "-disk-fed-id-controller",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-controller-sa",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "file",
			IdentityNameSuffix: "-file",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-file-fed-id-node",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa",
					Audience:       "openshift",
				},
				{
					CredentialName: clusterName + "-file-fed-id-operator",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-operator",
					Audience:       "openshift",
				},
				{
					CredentialName: clusterName + "-file-fed-id-controller",
					Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-controller-sa",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "imageRegistry",
			IdentityNameSuffix: "-image-registry",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-image-registry-fed-id-registry",
					Subject:        "system:serviceaccount:openshift-image-registry:registry",
					Audience:       "openshift",
				},
				{
					CredentialName: clusterName + "-image-registry-fed-id-operator",
					Subject:        "system:serviceaccount:openshift-image-registry:cluster-image-registry-operator",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "ingress",
			IdentityNameSuffix: "-ingress",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-ingress-fed-id",
					Subject:        "system:serviceaccount:openshift-ingress-operator:ingress-operator",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "cloudProvider",
			IdentityNameSuffix: "-cloud-provider",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-cloud-provider-fed-id",
					Subject:        "system:serviceaccount:kube-system:azure-cloud-provider",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "nodePoolManagement",
			IdentityNameSuffix: "-node-pool-mgmt",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-node-pool-mgmt-fed-id",
					Subject:        "system:serviceaccount:kube-system:capi-provider",
					Audience:       "openshift",
				},
			},
		},
		{
			ComponentName:      "network",
			IdentityNameSuffix: "-network",
			FederatedCredentials: []FederatedCredentialConfig{
				{
					CredentialName: clusterName + "-network-fed-id",
					Subject:        "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller",
					Audience:       "openshift",
				},
			},
		},
	}
}

// createFederatedIdentityCredential creates a federated identity credential for a managed identity using Azure SDK
func (i *IdentityManager) createFederatedIdentityCredential(ctx context.Context, l logr.Logger, resourceGroupName string, identityName string, oidcIssuerURL string, config FederatedCredentialConfig) error {
	l.Info("Creating federated identity credential",
		"credentialName", config.CredentialName,
		"identityName", identityName,
		"resourceGroup", resourceGroupName,
		"issuer", oidcIssuerURL,
		"subject", config.Subject,
		"audience", config.Audience)

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(i.cloud)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	clientOptions := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}}

	client, err := armmsi.NewFederatedIdentityCredentialsClient(i.subscriptionID, i.creds, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to create federated identity credentials client: %w", err)
	}

	_, err = client.CreateOrUpdate(ctx, resourceGroupName, identityName, config.CredentialName, armmsi.FederatedIdentityCredential{
		Properties: &armmsi.FederatedIdentityCredentialProperties{
			Issuer:    ptr.To(oidcIssuerURL),
			Subject:   ptr.To(config.Subject),
			Audiences: []*string{ptr.To(config.Audience)},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to create federated credential '%s': %w", config.CredentialName, err)
	}

	l.Info("Successfully created federated identity credential",
		"credentialName", config.CredentialName,
		"identityName", identityName,
		"resourceGroup", resourceGroupName,
		"issuer", oidcIssuerURL,
		"subject", config.Subject,
		"audience", config.Audience)

	return nil
}

// CreateWorkloadIdentitiesFromIAMOptions creates all managed identities and federated credentials
// for workload identity using CreateIAMOptions. This is used by the standalone IAM create command.
func (i *IdentityManager) CreateWorkloadIdentitiesFromIAMOptions(ctx context.Context, l logr.Logger, opts *CreateIAMOptions, resourceGroupName string) (*hyperv1.AzureWorkloadIdentities, error) {
	workloadIdentities := &hyperv1.AzureWorkloadIdentities{}
	definitions := GetWorkloadIdentityDefinitions(opts.Name)

	for _, def := range definitions {
		identityName := opts.Name + def.IdentityNameSuffix
		fullIdentityName := identityName + "-" + opts.InfraID

		// Create the managed identity
		_, clientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, identityName, opts.InfraID, opts.Location)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s managed identity: %w", def.ComponentName, err)
		}

		// Set the client ID on the appropriate workload identity field
		switch def.ComponentName {
		case "disk":
			workloadIdentities.Disk.ClientID = hyperv1.AzureClientID(clientID)
		case "file":
			workloadIdentities.File.ClientID = hyperv1.AzureClientID(clientID)
		case "imageRegistry":
			workloadIdentities.ImageRegistry.ClientID = hyperv1.AzureClientID(clientID)
		case "ingress":
			workloadIdentities.Ingress.ClientID = hyperv1.AzureClientID(clientID)
		case "cloudProvider":
			workloadIdentities.CloudProvider.ClientID = hyperv1.AzureClientID(clientID)
		case "nodePoolManagement":
			workloadIdentities.NodePoolManagement.ClientID = hyperv1.AzureClientID(clientID)
		case "network":
			workloadIdentities.Network.ClientID = hyperv1.AzureClientID(clientID)
		default:
			return nil, fmt.Errorf("unknown workload identity component: %s", def.ComponentName)
		}

		// Create federated credentials for this identity
		for _, cred := range def.FederatedCredentials {
			if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, fullIdentityName, opts.OIDCIssuerURL, cred); err != nil {
				return nil, err
			}
		}
	}

	return workloadIdentities, nil
}

// DestroyWorkloadIdentities deletes all managed identities and their federated credentials for a cluster.
// Federated credentials are explicitly deleted first, then the managed identity is deleted.
// The method continues deleting remaining identities even if some fail, logging errors as it goes.
func (i *IdentityManager) DestroyWorkloadIdentities(ctx context.Context, l logr.Logger, clusterName string, infraID string, resourceGroupName string) error {
	definitions := GetWorkloadIdentityDefinitions(clusterName)
	var errors []error

	for _, def := range definitions {
		identityName := clusterName + def.IdentityNameSuffix + "-" + infraID

		// First, delete all federated credentials for this identity
		for _, cred := range def.FederatedCredentials {
			if err := i.deleteFederatedIdentityCredential(ctx, l, resourceGroupName, identityName, cred.CredentialName); err != nil {
				l.Error(err, "Failed to delete federated credential, continuing",
					"credentialName", cred.CredentialName,
					"identityName", identityName)
				errors = append(errors, err)
			}
		}

		// Then delete the managed identity
		if err := i.deleteManagedIdentity(ctx, l, resourceGroupName, identityName); err != nil {
			l.Error(err, "Failed to delete managed identity, continuing with remaining identities",
				"identityName", identityName,
				"component", def.ComponentName)
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to delete %d resources during identity cleanup", len(errors))
	}

	l.Info("Successfully deleted all workload identities and federated credentials",
		"clusterName", clusterName,
		"infraID", infraID,
		"resourceGroup", resourceGroupName)

	return nil
}
