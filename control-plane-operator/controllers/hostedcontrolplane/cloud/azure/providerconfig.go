package azure

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

const (
	CloudConfigKey = "cloud.conf"
	Provider       = "azure"
)

// ReconcileCloudConfig reconciles as expected by Nodes Kubelet.
func ReconcileCloudConfig(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) error {
	cfg, err := azureConfigWithoutCredentials(hcp)
	if err != nil {
		return err
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

// ReconcileCloudConfigWithCredentials reconciles as expected by KAS/KCM.
func ReconcileCloudConfigWithCredentials(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane) error {
	cfg, err := azureConfigWithoutCredentials(hcp)
	if err != nil {
		return err
	}

	cfg.AADClientID = hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.CloudProvider.ClientID
	cfg.AADClientCertPath = config.ManagedAzureCertificatePath + hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.CloudProvider.CertificateName
	cfg.UseManagedIdentityExtension = false
	cfg.UseInstanceMetadata = false

	serializedConfig, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[CloudConfigKey] = serializedConfig
	return nil
}

func azureConfigWithoutCredentials(hcp *hyperv1.HostedControlPlane) (AzureConfig, error) {
	subnetName, err := azureutil.GetSubnetNameFromSubnetID(hcp.Spec.Platform.Azure.SubnetID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine subnet name from SubnetID: %w", err)
	}

	securityGroupName, securityGroupResourceGroup, err := azureutil.GetNameAndResourceGroupFromNetworkSecurityGroupID(hcp.Spec.Platform.Azure.SecurityGroupID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine security group name from SecurityGroupID: %w", err)
	}

	vnetName, vnetResourceGroup, err := azureutil.GetVnetNameAndResourceGroupFromVnetID(hcp.Spec.Platform.Azure.VnetID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine vnet name from VnetID: %w", err)
	}

	azureConfig := AzureConfig{
		Cloud:                        hcp.Spec.Platform.Azure.Cloud,
		TenantID:                     hcp.Spec.Platform.Azure.TenantID,
		UseManagedIdentityExtension:  true,
		SubscriptionID:               hcp.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:                hcp.Spec.Platform.Azure.ResourceGroupName,
		Location:                     hcp.Spec.Platform.Azure.Location,
		VnetName:                     vnetName,
		VnetResourceGroup:            vnetResourceGroup,
		SubnetName:                   subnetName,
		SecurityGroupName:            securityGroupName,
		SecurityGroupResourceGroup:   securityGroupResourceGroup,
		LoadBalancerName:             hcp.Spec.InfraID,
		CloudProviderBackoff:         true,
		CloudProviderBackoffDuration: 6,
		UseInstanceMetadata:          true,
		LoadBalancerSku:              "standard",
		DisableOutboundSNAT:          true,
	}

	return azureConfig, nil
}

// AzureConfig was originally a copy of the relevant subset of the upstream type
// at https://github.com/kubernetes/kubernetes/blob/30a21e9abdbbeb78d2b7ce59a79e46299ced2742/staging/src/k8s.io/legacy-cloud-providers/azure/azure.go#L123
// in order to not pick up the huge amount of transient dependencies that type pulls in.
// Now the source is https://github.com/kubernetes-sigs/cloud-provider-azure/blob/e5d670328a51e31787fc949ddf41a3efcd90d651/examples/out-of-tree/cloud-controller-manager.yaml#L232
// https://github.com/kubernetes-sigs/cloud-provider-azure/tree/e5d670328a51e31787fc949ddf41a3efcd90d651/pkg/provider/config
type AzureConfig struct {
	Cloud                        string `json:"cloud"`
	TenantID                     string `json:"tenantId"`
	UseManagedIdentityExtension  bool   `json:"useManagedIdentityExtension"`
	SubscriptionID               string `json:"subscriptionId"`
	AADClientID                  string `json:"aadClientId"`
	AADClientCertPath            string `json:"aadClientCertPath"`
	AADMSIDataPlaneIdentityPath  string `json:"aadMSIDataPlaneIdentityPath"`
	ResourceGroup                string `json:"resourceGroup"`
	Location                     string `json:"location"`
	VnetName                     string `json:"vnetName"`
	VnetResourceGroup            string `json:"vnetResourceGroup"`
	SubnetName                   string `json:"subnetName"`
	SecurityGroupName            string `json:"securityGroupName"`
	SecurityGroupResourceGroup   string `json:"securityGroupResourceGroup"`
	RouteTableName               string `json:"routeTableName"`
	CloudProviderBackoff         bool   `json:"cloudProviderBackoff"`
	CloudProviderBackoffDuration int    `json:"cloudProviderBackoffDuration"`
	UseInstanceMetadata          bool   `json:"useInstanceMetadata"`
	LoadBalancerSku              string `json:"loadBalancerSku"`
	DisableOutboundSNAT          bool   `json:"disableOutboundSNAT"`
	LoadBalancerName             string `json:"loadBalancerName"`
}
