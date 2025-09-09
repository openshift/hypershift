package azure

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/go-logr/logr"
	"k8s.io/utils/ptr"
)

// IdentityManager handles Azure managed identity and federated credential operations
type IdentityManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
}

// NewIdentityManager creates a new IdentityManager
func NewIdentityManager(subscriptionID string, creds azcore.TokenCredential) *IdentityManager {
	return &IdentityManager{
		subscriptionID: subscriptionID,
		creds:          creds,
	}
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

	client, err := armmsi.NewUserAssignedIdentitiesClient(i.subscriptionID, i.creds, nil)
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

// createFederatedIdentityCredential creates a federated identity credential for a managed identity using Azure SDK
func (i *IdentityManager) createFederatedIdentityCredential(ctx context.Context, l logr.Logger, resourceGroupName string, identityName string, oidcIssuerURL string, config FederatedCredentialConfig) error {
	l.Info("Creating federated identity credential",
		"credentialName", config.CredentialName,
		"identityName", identityName,
		"resourceGroup", resourceGroupName,
		"issuer", oidcIssuerURL,
		"subject", config.Subject,
		"audience", config.Audience)

	client, err := armmsi.NewFederatedIdentityCredentialsClient(i.subscriptionID, i.creds, nil)
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

// CreateWorkloadIdentities creates all managed identities and federated credentials for workload identity
func (i *IdentityManager) CreateWorkloadIdentities(ctx context.Context, l logr.Logger, opts *CreateInfraOptions, resourceGroupName string) (*hyperv1.AzureWorkloadIdentities, error) {
	workloadIdentities := &hyperv1.AzureWorkloadIdentities{}

	// Create Azure Disk managed identity
	diskIdentityName := fmt.Sprintf("%s-disk", opts.Name)
	_, diskClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, diskIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create disk managed identity: %w", err)
	}
	workloadIdentities.Disk.ClientID = hyperv1.AzureClientID(diskClientID)

	// Create federated credentials for disk identity
	diskCredentials := []FederatedCredentialConfig{
		{
			CredentialName: diskIdentityName + "-fed-id-node",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
			Audience:       "openshift",
		},
		{
			CredentialName: diskIdentityName + "-fed-id-operator",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator",
			Audience:       "openshift",
		},
		{
			CredentialName: diskIdentityName + "-fed-id-controller",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-controller-sa",
			Audience:       "openshift",
		},
	}

	for _, cred := range diskCredentials {
		if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, diskIdentityName+"-"+opts.InfraID, opts.OIDCIssuerURL, cred); err != nil {
			return nil, err
		}
	}

	// Create Azure File managed identity
	fileIdentityName := fmt.Sprintf("%s-file", opts.Name)
	_, fileClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, fileIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create file managed identity: %w", err)
	}
	workloadIdentities.File.ClientID = hyperv1.AzureClientID(fileClientID)

	// Create federated credentials for file identity
	fileCredentials := []FederatedCredentialConfig{
		{
			CredentialName: fileIdentityName + "-fed-id-node",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa",
			Audience:       "openshift",
		},
		{
			CredentialName: fileIdentityName + "-fed-id-operator",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-operator",
			Audience:       "openshift",
		},
		{
			CredentialName: fileIdentityName + "-fed-id-controller",
			Subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-controller-sa",
			Audience:       "openshift",
		},
	}

	for _, cred := range fileCredentials {
		if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, fileIdentityName+"-"+opts.InfraID, opts.OIDCIssuerURL, cred); err != nil {
			return nil, err
		}
	}

	// Create Image Registry managed identity
	imageRegistryIdentityName := fmt.Sprintf("%s-image-registry", opts.Name)
	_, imageRegistryClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, imageRegistryIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create image registry managed identity: %w", err)
	}
	workloadIdentities.ImageRegistry.ClientID = hyperv1.AzureClientID(imageRegistryClientID)

	// Create federated credentials for image registry identity
	imageRegistryCredentials := []FederatedCredentialConfig{
		{
			CredentialName: imageRegistryIdentityName + "-fed-id-registry",
			Subject:        "system:serviceaccount:openshift-image-registry:registry",
			Audience:       "openshift",
		},
		{
			CredentialName: imageRegistryIdentityName + "-fed-id-operator",
			Subject:        "system:serviceaccount:openshift-image-registry:cluster-image-registry-operator",
			Audience:       "openshift",
		},
	}

	imageRegIdentityName := imageRegistryIdentityName + "-" + opts.InfraID
	for _, cred := range imageRegistryCredentials {
		if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, imageRegIdentityName, opts.OIDCIssuerURL, cred); err != nil {
			return nil, err
		}
	}

	// Create Ingress managed identity
	ingressIdentityName := fmt.Sprintf("%s-ingress", opts.Name)
	_, ingressClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, ingressIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create ingress managed identity: %w", err)
	}
	workloadIdentities.Ingress.ClientID = hyperv1.AzureClientID(ingressClientID)

	// Create federated credential for ingress identity
	ingressCredential := FederatedCredentialConfig{
		CredentialName: ingressIdentityName + "-fed-id",
		Subject:        "system:serviceaccount:openshift-ingress-operator:ingress-operator",
		Audience:       "openshift",
	}

	if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, ingressIdentityName+"-"+opts.InfraID, opts.OIDCIssuerURL, ingressCredential); err != nil {
		return nil, err
	}

	// Create Cloud Provider managed identity
	cloudProviderIdentityName := fmt.Sprintf("%s-cloud-provider", opts.Name)
	_, cloudProviderClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, cloudProviderIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud provider managed identity: %w", err)
	}
	workloadIdentities.CloudProvider.ClientID = hyperv1.AzureClientID(cloudProviderClientID)

	// Create federated credential for cloud provider identity
	cloudProviderCredential := FederatedCredentialConfig{
		CredentialName: cloudProviderIdentityName + "-fed-id",
		Subject:        "system:serviceaccount:kube-system:azure-cloud-provider",
		Audience:       "openshift",
	}

	cpIdentityName := cloudProviderIdentityName + "-" + opts.InfraID
	if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, cpIdentityName, opts.OIDCIssuerURL, cloudProviderCredential); err != nil {
		return nil, err
	}

	// Create Node Pool Management managed identity
	nodePoolMgmtIdentityName := fmt.Sprintf("%s-node-pool-mgmt", opts.Name)
	_, nodePoolMgmtClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, nodePoolMgmtIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create node pool management managed identity: %w", err)
	}
	workloadIdentities.NodePoolManagement.ClientID = hyperv1.AzureClientID(nodePoolMgmtClientID)

	// Create federated credential for node pool management identity
	nodePoolMgmtCredential := FederatedCredentialConfig{
		CredentialName: nodePoolMgmtIdentityName + "-fed-id",
		Subject:        "system:serviceaccount:kube-system:capi-provider",
		Audience:       "openshift",
	}

	npMgmtIdentityName := nodePoolMgmtIdentityName + "-" + opts.InfraID
	if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, npMgmtIdentityName, opts.OIDCIssuerURL, nodePoolMgmtCredential); err != nil {
		return nil, err
	}

	// Create Network managed identity
	networkIdentityName := fmt.Sprintf("%s-network", opts.Name)
	_, networkClientID, _, err := i.createManagedIdentity(ctx, l, resourceGroupName, networkIdentityName, opts.InfraID, opts.Location)
	if err != nil {
		return nil, fmt.Errorf("failed to create network managed identity: %w", err)
	}
	workloadIdentities.Network.ClientID = hyperv1.AzureClientID(networkClientID)

	// Create federated credential for network identity
	networkCredential := FederatedCredentialConfig{
		CredentialName: networkIdentityName + "-fed-id",
		Subject:        "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller",
		Audience:       "openshift",
	}

	networkIDName := networkIdentityName + "-" + opts.InfraID
	if err := i.createFederatedIdentityCredential(ctx, l, resourceGroupName, networkIDName, opts.OIDCIssuerURL, networkCredential); err != nil {
		return nil, err
	}

	return workloadIdentities, nil
}
