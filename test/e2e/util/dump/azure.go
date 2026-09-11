package dump

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/go-logr/logr"
)

// DumpAzureLBStatus dumps the state of all Azure Load Balancers in the hosted
// cluster's resource group to azure-lb-status.json in the artifact directory.
// This is useful for diagnosing networking issues where traffic is not reaching
// the hosted cluster (e.g. konnectivity or ingress failures caused by Azure LB
// misconfiguration or missing backend pool members).
func DumpAzureLBStatus(ctx context.Context, hc *hyperv1.HostedCluster, azureCredentialsFile string, artifactDir string) error {
	if hc.Spec.Platform.Azure == nil {
		return fmt.Errorf("hosted cluster %s/%s does not have Azure platform configuration", hc.Namespace, hc.Name)
	}

	subscriptionID := hc.Spec.Platform.Azure.SubscriptionID
	resourceGroupName := hc.Spec.Platform.Azure.ResourceGroupName
	cloudName := hc.Spec.Platform.Azure.Cloud

	if subscriptionID == "" || resourceGroupName == "" {
		return fmt.Errorf("hosted cluster %s/%s is missing Azure subscriptionID or resourceGroupName", hc.Namespace, hc.Name)
	}

	azureCreds, err := setupCredentials(azureCredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to set up Azure credentials: %w", err)
	}

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(cloudName)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration for %q: %w", cloudName, err)
	}

	clientOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloudConfig,
		},
	}

	lbClient, err := armnetwork.NewLoadBalancersClient(subscriptionID, azureCreds, clientOpts)
	if err != nil {
		return fmt.Errorf("failed to create Azure LoadBalancers client: %w", err)
	}

	var loadBalancers []*armnetwork.LoadBalancer
	pager := lbClient.NewListPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list load balancers in resource group %s: %w", resourceGroupName, err)
		}
		loadBalancers = append(loadBalancers, page.Value...)
	}

	output := map[string]interface{}{
		"subscriptionID":    subscriptionID,
		"resourceGroupName": resourceGroupName,
		"cloud":             cloudName,
		"hostedCluster":     fmt.Sprintf("%s/%s", hc.Namespace, hc.Name),
		"infraID":           hc.Spec.InfraID,
		"loadBalancerCount": len(loadBalancers),
		"loadBalancers":     loadBalancers,
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal load balancer status: %w", err)
	}

	outputPath := filepath.Join(artifactDir, "azure-lb-status.json")
	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	return nil
}

// setupCredentials reads Azure credentials from the given file and returns
// a TokenCredential suitable for ARM client creation. This follows the same
// pattern as cmd/util.SetupAzureCredentials but without requiring a logger.
func setupCredentials(credentialsFile string) (azcore.TokenCredential, error) {
	_, creds, err := cmdutil.SetupAzureCredentials(logr.Discard(), nil, credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to setup Azure credentials from %s: %w", credentialsFile, err)
	}
	return creds, nil
}

