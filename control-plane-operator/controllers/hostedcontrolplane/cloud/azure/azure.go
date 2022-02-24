package azure

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const CloudConfigKey = "cloud.conf"

func ReconcileCloudConfig(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane, credentialsSecret *corev1.Secret) error {
	cfg := AzureConfig{
		Cloud:                        "AzurePublicCloud",
		TenantID:                     string(credentialsSecret.Data["AZURE_TENANT_ID"]),
		UseManagedIdentityExtension:  true,
		SubscriptionID:               hcp.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:                hcp.Spec.Platform.Azure.ResourceGroupName,
		Location:                     hcp.Spec.Platform.Azure.Location,
		VnetName:                     hcp.Spec.Platform.Azure.VnetName,
		VnetResourceGroup:            hcp.Spec.Platform.Azure.ResourceGroupName,
		SubnetName:                   hcp.Spec.Platform.Azure.SubnetName,
		SecurityGroupName:            hcp.Spec.Platform.Azure.SecurityGroupName,
		CloudProviderBackoff:         true,
		CloudProviderBackoffDuration: 6,
		UseInstanceMetadata:          true,
		LoadBalancerSku:              "standard",
	}
	serializedConfig, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[CloudConfigKey] = string(serializedConfig)

	return nil
}

// AzureConfig is a copy of the relevant subset of the upstream type
// at https://github.com/kubernetes/kubernetes/blob/30a21e9abdbbeb78d2b7ce59a79e46299ced2742/staging/src/k8s.io/legacy-cloud-providers/azure/azure.go#L123
// in order to not pick up the huge amount of transient dependencies that type pulls in.
type AzureConfig struct {
	Cloud                        string `json:"cloud"`
	TenantID                     string `json:"tenantId"`
	UseManagedIdentityExtension  bool   `json:"useManagedIdentityExtension"`
	SubscriptionID               string `json:"subscriptionId"`
	ResourceGroup                string `json:"resourceGroup"`
	Location                     string `json:"location"`
	VnetName                     string `json:"vnetName"`
	VnetResourceGroup            string `json:"vnetResourceGroup"`
	SubnetName                   string `json:"subnetName"`
	SecurityGroupName            string `json:"securityGroupName"`
	RouteTableName               string `json:"routeTableName"`
	CloudProviderBackoff         bool   `json:"cloudProviderBackoff"`
	CloudProviderBackoffDuration int    `json:"cloudProviderBackoffDuration"`
	UseInstanceMetadata          bool   `json:"useInstanceMetadata"`
	LoadBalancerSku              string `json:"loadBalancerSku"`
}
