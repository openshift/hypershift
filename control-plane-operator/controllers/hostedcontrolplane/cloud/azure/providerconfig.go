package azure

const (
	CloudConfigKey = "cloud.conf"
	Provider       = "azure"
)

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
