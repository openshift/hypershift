package azure

import (
	"encoding/json"
	"fmt"
	"strconv"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/secretproviderclass"

	corev1 "k8s.io/api/core/v1"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

const (
	ConfigKey                                  = "cloud.conf"
	loadBalancerHealthProbeModeShared          = "shared"
	loadBalancerHealthProbeModeServiceNodePort = "servicenodeport"
)

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	cfg, err := azureConfig(cpContext, false)
	if err != nil {
		return err
	}

	serializedConfig, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	cm.Data[ConfigKey] = string(serializedConfig)
	return nil
}

func adaptConfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	cfg, err := azureConfig(cpContext, true)
	if err != nil {
		return err
	}

	if azureutil.IsAroHCP() {
		cfg.UseManagedIdentityExtension = false
		cfg.UseInstanceMetadata = false
	}

	serializedConfig, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	secret.Data[ConfigKey] = serializedConfig
	return nil
}

func adaptSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	if azureutil.IsAroHCP() {
		secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, cpContext.HCP.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.CloudProvider)
	}
	return nil
}

func azureConfig(cpContext component.WorkloadContext, withCredentials bool) (AzureConfig, error) {
	hcp := cpContext.HCP
	azureplatform := hcp.Spec.Platform.Azure

	subnetName, err := azureutil.GetSubnetNameFromSubnetID(azureplatform.SubnetID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine subnet name from SubnetID: %w", err)
	}

	securityGroupName, securityGroupResourceGroup, err := azureutil.GetNameAndResourceGroupFromNetworkSecurityGroupID(azureplatform.SecurityGroupID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine security group name from SecurityGroupID: %w", err)
	}

	vnetName, vnetResourceGroup, err := azureutil.GetVnetNameAndResourceGroupFromVnetID(azureplatform.VnetID)
	if err != nil {
		return AzureConfig{}, fmt.Errorf("failed to determine vnet name from VnetID: %w", err)
	}

	probeMode := loadBalancerHealthProbeModeShared
	var (
		probePath string
		probePort int32
	)

	// Check for annotation overrides
	if mode, ok := hcp.Annotations[hyperv1.AzureLoadBalancerHealthProbeModeAnnotation]; ok {
		if mode == loadBalancerHealthProbeModeShared || mode == loadBalancerHealthProbeModeServiceNodePort {
			probeMode = mode
		} else {
			return AzureConfig{}, fmt.Errorf("invalid value for annotation %s: %s (valid values: %s, %s)", hyperv1.AzureLoadBalancerHealthProbeModeAnnotation, mode, loadBalancerHealthProbeModeShared, loadBalancerHealthProbeModeServiceNodePort)
		}
	}

	// Check for shared load balancer health probe path annotation (only applies when mode is shared)
	if path, ok := hcp.Annotations[hyperv1.SharedLoadBalancerHealthProbePathAnnotation]; ok {
		if probeMode == loadBalancerHealthProbeModeShared {
			probePath = path
		}
	}

	// Check for shared load balancer health probe port annotation (only applies when mode is shared)
	if portStr, ok := hcp.Annotations[hyperv1.SharedLoadBalancerHealthProbePortAnnotation]; ok {
		if probeMode == loadBalancerHealthProbeModeShared {
			portNum, err := strconv.Atoi(portStr)
			if err != nil {
				return AzureConfig{}, fmt.Errorf("invalid value for annotation %s: %s (must be a valid port number)", hyperv1.SharedLoadBalancerHealthProbePortAnnotation, portStr)
			}
			if portNum < 1 || portNum > 65535 {
				return AzureConfig{}, fmt.Errorf("invalid value for annotation %s: %d (must be between 1 and 65535)", hyperv1.SharedLoadBalancerHealthProbePortAnnotation, portNum)
			}
			probePort = int32(portNum)
		}
	}

	azureConfig := AzureConfig{
		Cloud:                        azureplatform.Cloud,
		TenantID:                     azureplatform.TenantID,
		SubscriptionID:               azureplatform.SubscriptionID,
		ResourceGroup:                azureplatform.ResourceGroupName,
		Location:                     azureplatform.Location,
		VnetName:                     vnetName,
		VnetResourceGroup:            vnetResourceGroup,
		SubnetName:                   subnetName,
		SecurityGroupName:            securityGroupName,
		SecurityGroupResourceGroup:   securityGroupResourceGroup,
		LoadBalancerName:             hcp.Spec.InfraID,
		CloudProviderBackoff:         true,
		CloudProviderBackoffDuration: 6,
		LoadBalancerSku:              "standard",
		DisableOutboundSNAT:          true,
		ClusterServiceLoadBalancerHealthProbeMode: probeMode,
		UseInstanceMetadata:                       true,
	}
	if probePath != "" {
		azureConfig.ClusterServiceSharedLoadBalancerHealthProbePath = probePath
	}
	if probePort != 0 {
		azureConfig.ClusterServiceSharedLoadBalancerHealthProbePort = probePort
	}

	// Configure authentication method based on platform type
	if azureutil.IsAroHCP() {
		// ARO HCP uses managed identity
		azureConfig.UseManagedIdentityExtension = true
	} else if azureutil.IsSelfManagedAzure(hcp.Spec.Platform.Type) {
		// Self-managed Azure uses workload identity
		azureConfig.UseFederatedWorkloadIdentityExtension = true
		azureConfig.AADClientID = string(azureplatform.AzureAuthenticationConfig.WorkloadIdentities.CloudProvider.ClientID)
		azureConfig.AADFederatedTokenFile = "/var/run/secrets/openshift/serviceaccount/token"
	}

	if withCredentials {
		if azureutil.IsAroHCP() {
			azureConfig.AADMSIDataPlaneIdentityPath = config.ManagedAzureCertificatePath + hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.CloudProvider.CredentialsSecretName
		}
	}

	return azureConfig, nil
}

// AzureConfig was originally a copy of the relevant subset of the upstream type
// at https://github.com/kubernetes/kubernetes/blob/30a21e9abdbbeb78d2b7ce59a79e46299ced2742/staging/src/k8s.io/legacy-cloud-providers/azure/azure.go#L123
// in order to not pick up the huge amount of transient dependencies that type pulls in.
// Now the source is https://github.com/kubernetes-sigs/cloud-provider-azure/blob/e5d670328a51e31787fc949ddf41a3efcd90d651/examples/out-of-tree/cloud-controller-manager.yaml#L232
// https://github.com/kubernetes-sigs/cloud-provider-azure/tree/e5d670328a51e31787fc949ddf41a3efcd90d651/pkg/provider/config
type AzureConfig struct {
	Cloud                                 string `json:"cloud"`
	TenantID                              string `json:"tenantId"`
	UseManagedIdentityExtension           bool   `json:"useManagedIdentityExtension"`
	UseFederatedWorkloadIdentityExtension bool   `json:"useFederatedWorkloadIdentityExtension"`
	SubscriptionID                        string `json:"subscriptionId"`
	AADClientID                           string `json:"aadClientId"`
	// TODO HOSTEDCP-1542 - Bryan - drop client secret once we have WorkloadIdentity working
	AADClientSecret                                 string `json:"aadClientSecret"`
	AADClientCertPath                               string `json:"aadClientCertPath"`
	AADFederatedTokenFile                           string `json:"aadFederatedTokenFile"`
	AADMSIDataPlaneIdentityPath                     string `json:"aadMSIDataPlaneIdentityPath"`
	ResourceGroup                                   string `json:"resourceGroup"`
	Location                                        string `json:"location"`
	VnetName                                        string `json:"vnetName"`
	VnetResourceGroup                               string `json:"vnetResourceGroup"`
	SubnetName                                      string `json:"subnetName"`
	SecurityGroupName                               string `json:"securityGroupName"`
	SecurityGroupResourceGroup                      string `json:"securityGroupResourceGroup"`
	RouteTableName                                  string `json:"routeTableName"`
	CloudProviderBackoff                            bool   `json:"cloudProviderBackoff"`
	CloudProviderBackoffDuration                    int    `json:"cloudProviderBackoffDuration"`
	UseInstanceMetadata                             bool   `json:"useInstanceMetadata"`
	LoadBalancerSku                                 string `json:"loadBalancerSku"`
	DisableOutboundSNAT                             bool   `json:"disableOutboundSNAT"`
	LoadBalancerName                                string `json:"loadBalancerName"`
	ClusterServiceLoadBalancerHealthProbeMode       string `json:"clusterServiceLoadBalancerHealthProbeMode"`
	ClusterServiceSharedLoadBalancerHealthProbePath string `json:"clusterServiceSharedLoadBalancerHealthProbePath,omitempty"`
	ClusterServiceSharedLoadBalancerHealthProbePort int32  `json:"clusterServiceSharedLoadBalancerHealthProbePort,omitempty"`
}
