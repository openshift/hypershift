package util

import (
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/go-logr/logr"
	"sigs.k8s.io/yaml"
)

// AzureCreds is the file format we expect for credentials. It is copied from the installer
// to allow using the same credentials file for both:
// https://github.com/openshift/installer/blob/8fca1ade5b096d9b2cd312c4599881d099439288/pkg/asset/installconfig/azure/session.go#L36
type AzureCreds struct {
	SubscriptionID string `json:"subscriptionId,omitempty"`
	ClientID       string `json:"clientId,omitempty"`
	ClientSecret   string `json:"clientSecret,omitempty"`
	TenantID       string `json:"tenantId,omitempty"`
}

// ManagedIdentityInfo is the file format we expect to get the managed identity client ID and its cert name for each
// control plane component that needs to authenticate with Azure
type ManagedIdentityInfo struct {
	Name     string `json:"name"`
	ClientID string `json:"clientId"`
	CertName string `json:"certName"`
}

// SetupAzureCredentials creates the Azure credentials needed to create Azure resources from credentials passed in from the user or from a credentials file
func SetupAzureCredentials(l logr.Logger, credentials *AzureCreds, credentialsFile string) (string, *azidentity.DefaultAzureCredential, error) {
	creds := credentials
	if creds == nil {
		var err error
		creds, err = ReadCredentials(credentialsFile)
		if err != nil {
			return "", nil, fmt.Errorf("failed to read the credentials: %w", err)
		}
		l.Info("Using credentials from file", "path", credentialsFile)
	}

	_ = os.Setenv("AZURE_TENANT_ID", creds.TenantID)
	_ = os.Setenv("AZURE_CLIENT_ID", creds.ClientID)
	_ = os.Setenv("AZURE_CLIENT_SECRET", creds.ClientSecret)
	azureCreds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Azure credentials to create image gallery: %w", err)
	}

	return creds.SubscriptionID, azureCreds, nil
}

// ReadCredentials reads a file with azure credentials and returns it as a struct
func ReadCredentials(path string) (*AzureCreds, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read from %s: %w", path, err)
	}

	var result AzureCreds
	if err := yaml.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return &result, nil
}

// ValidateMarketplaceFlags validates if any marketplace flag was used, all were set to a non-empty value
func ValidateMarketplaceFlags(marketplaceFlags map[string]*string) error {
	allFlagsEmpty := true
	for _, value := range marketplaceFlags {
		if value != nil && *value != "" {
			allFlagsEmpty = false
			break
		}
	}

	// It is okay if all the flags are empty, meaning an ImageID was used instead of an Azure Marketplace image.
	if allFlagsEmpty {
		return nil
	}

	// If one marketplace flag was used, ensure all were to be set with non-empty values.
	for flag, value := range marketplaceFlags {
		if value == nil || *value == "" {
			return fmt.Errorf("all marketplace flags (i.e. marketplace-publisher, marketplace-offer, marketplace-sku, marketplace-version) are required when using a marketplace image; the following flag was empty: %s", flag)
		}
	}

	return nil
}

// readManagedIdentityConfiguration reads a file with managed identity info and returns it as a slice of type ManagedIdentityInfo
func readManagedIdentityConfiguration(path string) ([]ManagedIdentityInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read from %s: %w", path, err)
	}

	var result []ManagedIdentityInfo
	if err := yaml.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return result, nil
}

func SetupManagedIdentityCredentials(ManagedIdentityConfigurationFile string, hc *hyperv1.HostedCluster) error {
	managedIdentityCredentials, err := readManagedIdentityConfiguration(ManagedIdentityConfigurationFile)
	if err != nil {
		return fmt.Errorf("failed to read managed identity configuration file: %w", err)
	}

	for _, managedIdentityInfo := range managedIdentityCredentials {
		switch managedIdentityInfo.Name {
		case "azure-disk":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Disk.CertificateName = managedIdentityInfo.CertName
		case "azure-file":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.File.CertificateName = managedIdentityInfo.CertName
		case "capz":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ClusterAPIAzure.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ClusterAPIAzure.CertificateName = managedIdentityInfo.CertName
		case "cloud-provider":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.CloudProvider.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.CloudProvider.CertificateName = managedIdentityInfo.CertName
		case "cncc":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Network.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Network.CertificateName = managedIdentityInfo.CertName
		case "control-plane":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ControlPlane.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ControlPlane.CertificateName = managedIdentityInfo.CertName
		case "image-registry":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ImageRegistry.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ImageRegistry.CertificateName = managedIdentityInfo.CertName
		case "ingress":
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Ingress.ClientID = managedIdentityInfo.ClientID
			hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Ingress.CertificateName = managedIdentityInfo.CertName
		}
	}
	return nil
}
