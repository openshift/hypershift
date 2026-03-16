package util

import (
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/openshift/hypershift/support/azureutil"
	"sigs.k8s.io/yaml"
)

// AzureCreds is the file format we expect for credentials.
type AzureCreds struct {
	SubscriptionID string `json:"subscriptionId,omitempty"`
	ClientID       string `json:"clientId,omitempty"`
	ClientSecret   string `json:"clientSecret,omitempty"`
	TenantID       string `json:"tenantId,omitempty"`
}

func AzureComputeClient(cloudName, credsFile string) (string, *armcompute.VirtualMachinesClient, error) {
	subscriptionID, creds, err := SetupAzureCredentials(credsFile)
	if err != nil {
		return "", nil, err
	}

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(cloudName)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get Azure cloud configuration: %v", err)
	}

	client, err := armcompute.NewVirtualMachinesClient(subscriptionID, creds, azureutil.NewARMClientOptions(cloudConfig))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Azure compute client: %v", err)
	}

	return subscriptionID, client, nil
}

func SetupAzureCredentials(credentialsFile string) (string, azcore.TokenCredential, error) {
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read from %s: %w", credentialsFile, err)
	}

	var creds AzureCreds
	if err := yaml.Unmarshal(raw, &creds); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	_ = os.Setenv("AZURE_TENANT_ID", creds.TenantID)
	_ = os.Setenv("AZURE_CLIENT_ID", creds.ClientID)
	_ = os.Setenv("AZURE_CLIENT_SECRET", creds.ClientSecret)
	azureCreds, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create Azure credentials: %w", err)
	}

	return creds.SubscriptionID, azureCreds, nil
}

