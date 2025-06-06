package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

type Azure struct {
	capiProviderImage string
	payloadVersion    *semver.Version
}

func New(capiProviderImage string, payloadVersion *semver.Version) *Azure {
	if payloadVersion != nil {
		payloadVersion.Pre = nil
		payloadVersion.Build = nil
	}

	return &Azure{
		capiProviderImage: capiProviderImage,
		payloadVersion:    payloadVersion,
	}
}

func (a Azure) ReconcileCAPIInfraCR(
	ctx context.Context,
	c client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string,
	apiEndpoint hyperv1.APIEndpoint,
) (client.Object, error) {
	azureCluster := &capiazure.AzureCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcluster.Name,
			Namespace: controlPlaneNamespace,
		},
	}

	azureClusterIdentity := &capiazure.AzureClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcluster.Name,
			Namespace: controlPlaneNamespace,
		},
	}

	if _, err := createOrUpdate(ctx, c, azureClusterIdentity, func() error {
		return reconcileAzureClusterIdentity(hcluster, azureClusterIdentity, controlPlaneNamespace, a.payloadVersion)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Azure cluster identity: %w", err)
	}

	if _, err := createOrUpdate(ctx, c, azureCluster, func() error {
		return reconcileAzureCluster(azureCluster, hcluster, apiEndpoint, azureClusterIdentity, controlPlaneNamespace)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Azure CAPI cluster: %w", err)
	}

	return azureCluster, nil
}

func (a Azure) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	image := a.capiProviderImage
	if envImage := os.Getenv(images.AzureCAPIProviderEnvVar); len(envImage) > 0 {
		image = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIAzureProviderImage]; ok {
		image = override
	}
	defaultMode := int32(0640)
	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: ptr.To[int64](10),
				Containers: []corev1.Container{{
					Name:            "manager",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args: []string{
						"--namespace=$(MY_NAMESPACE)",
						"--leader-elect=true",
						"--feature-gates=MachinePool=false,ASOAPI=false",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("10Mi"),
						},
					},
					Env: []corev1.EnvVar{
						{
							Name: "MY_NAMESPACE",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.namespace",
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "capi-webhooks-tls",
							MountPath: "/tmp/k8s-webhook-server/serving-certs",
							ReadOnly:  true,
						},
						{
							Name:      "svc-kubeconfig",
							MountPath: "/etc/kubernetes",
						},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "capi-webhooks-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: "capi-webhooks-tls",
							},
						},
					},
					{
						Name: "svc-kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "service-network-admin-kubeconfig",
							},
						},
					},
				},
			}}}

	if azureutil.IsAroHCP() {
		managedAzureKeyVaultManagedIdentityClientID, ok := os.LookupEnv(config.AROHCPKeyVaultManagedIdentityClientID)
		if !ok {
			return nil, fmt.Errorf("environment variable %s is not set", config.AROHCPKeyVaultManagedIdentityClientID)
		}

		deploymentSpec.Template.Spec.Containers[0].Env = append(deploymentSpec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  config.AROHCPKeyVaultManagedIdentityClientID,
				Value: managedAzureKeyVaultManagedIdentityClientID,
			},
		)

		deploymentSpec.Template.Spec.Containers[0].VolumeMounts = append(deploymentSpec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      config.ManagedAzureNodePoolMgmtSecretStoreVolumeName,
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			},
		)

		deploymentSpec.Template.Spec.Volumes = append(deploymentSpec.Template.Spec.Volumes,
			corev1.Volume{
				Name: config.ManagedAzureNodePoolMgmtSecretStoreVolumeName,
				VolumeSource: corev1.VolumeSource{
					CSI: &corev1.CSIVolumeSource{
						Driver:   config.ManagedAzureSecretsStoreCSIDriver,
						ReadOnly: ptr.To(true),
						VolumeAttributes: map[string]string{
							config.ManagedAzureSecretProviderClass: config.ManagedAzureNodePoolMgmtSecretProviderClassName,
						},
					},
				},
			},
		)
	}

	return deploymentSpec, nil
}

func (a Azure) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	// Sync CNCC secret
	cloudNetworkConfigCreds := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "cloud-network-config-controller-creds"}}
	secretData := map[string][]byte{
		"azure_region":          []byte(hcluster.Spec.Platform.Azure.Location),
		"azure_resource_prefix": []byte(hcluster.Name + "-" + hcluster.Spec.InfraID),
		"azure_resourcegroup":   []byte(hcluster.Spec.Platform.Azure.ResourceGroupName),
		"azure_subscription_id": []byte(hcluster.Spec.Platform.Azure.SubscriptionID),
		"azure_tenant_id":       []byte(hcluster.Spec.Platform.Azure.TenantID),
	}
	if _, err := createOrUpdate(ctx, c, cloudNetworkConfigCreds, func() error {
		cloudNetworkConfigCreds.Data = secretData
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (a Azure) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hc *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	// Reconcile the Azure KMS Config Secret
	azureKMSConfigSecret := manifests.AzureKMSWithCredentials(controlPlaneNamespace)
	if _, err := createOrUpdate(ctx, c, azureKMSConfigSecret, func() error {
		return reconcileKMSConfigSecret(azureKMSConfigSecret, hc)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Azure KMS config secret: %w", err)
	}
	return nil
}

func (a Azure) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}

func (a Azure) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func reconcileAzureCluster(azureCluster *capiazure.AzureCluster, hcluster *hyperv1.HostedCluster, apiEndpoint hyperv1.APIEndpoint, azureClusterIdentity *capiazure.AzureClusterIdentity, controlPlaneNamespace string) error {
	if azureCluster.Annotations == nil {
		azureCluster.Annotations = map[string]string{}
	}
	azureCluster.Annotations[capiv1.ManagedByAnnotation] = "external"

	vnetName, vnetResourceGroup, err := azureutil.GetVnetNameAndResourceGroupFromVnetID(hcluster.Spec.Platform.Azure.VnetID)
	if err != nil {
		return err
	}

	azureCluster.Spec.Location = hcluster.Spec.Platform.Azure.Location
	azureCluster.Spec.ResourceGroup = hcluster.Spec.Platform.Azure.ResourceGroupName
	azureCluster.Spec.NetworkSpec.Vnet.ID = hcluster.Spec.Platform.Azure.VnetID
	azureCluster.Spec.NetworkSpec.Vnet.Name = vnetName
	azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup = vnetResourceGroup
	azureCluster.Spec.SubscriptionID = hcluster.Spec.Platform.Azure.SubscriptionID
	azureCluster.Spec.NetworkSpec.NodeOutboundLB = &capiazure.LoadBalancerSpec{}
	azureCluster.Spec.NetworkSpec.NodeOutboundLB.Name = hcluster.Spec.InfraID
	azureCluster.Spec.NetworkSpec.NodeOutboundLB.BackendPool.Name = hcluster.Spec.InfraID

	azureCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}

	azureCluster.Status.Ready = true

	azureCluster.Spec.IdentityRef = &corev1.ObjectReference{Name: azureClusterIdentity.Name, Namespace: azureClusterIdentity.Namespace}

	return nil
}

// reconcileAzureClusterIdentity creates a CAPZ AzureClusterIdentity custom resource using UserAssignedIdentityCredentials
// for managed Azure deployments, aka ARO HCP, as the Azure authentication method. More information on this custom
// resource type can be found here: https://capz.sigs.k8s.io/topics/identities.
//
// For non-managed Azure deployments, the AzureClusterIdentity is created using WorkloadIdentity.
func reconcileAzureClusterIdentity(hc *hyperv1.HostedCluster, azureClusterIdentity *capiazure.AzureClusterIdentity, controlPlaneNamespace string, payloadVersion *semver.Version) error {
	if azureutil.IsAroHCP() {
		azureCloudType, err := parseCloudType(hc.Spec.Platform.Azure.Cloud)
		if err != nil {
			return err
		}

		// Create a AzureClusterIdentity with the Azure authentication type UserAssignedIdentityCredentials
		azureClusterIdentity.Spec = capiazure.AzureClusterIdentitySpec{
			TenantID:                                 hc.Spec.Platform.Azure.TenantID,
			UserAssignedIdentityCredentialsCloudType: azureCloudType,
			UserAssignedIdentityCredentialsPath:      config.ManagedAzureCertificatePath + hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.NodePoolManagement.CredentialsSecretName,
			Type:                                     capiazure.UserAssignedIdentityCredential,
			AllowedNamespaces: &capiazure.AllowedNamespaces{
				NamespaceList: []string{
					controlPlaneNamespace,
				},
			},
		}
		return nil
	} else {
		if hc.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities != nil {
			azureClusterIdentity.Spec = capiazure.AzureClusterIdentitySpec{
				ClientID: hc.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.CAPZClientID,
				TenantID: hc.Spec.Platform.Azure.TenantID,
				Type:     capiazure.WorkloadIdentity,
				AllowedNamespaces: &capiazure.AllowedNamespaces{
					NamespaceList: []string{
						controlPlaneNamespace,
					},
				},
			}
			return nil
		}
		return fmt.Errorf("WorkloadIdentities must be set in the Azure platform spec for non-managed Azure deployments")
	}
}

// parseCloudType translates the HyperShift APIs valid cloud values to valid CAPZ cloud values
//
// NOTE - HyperShift accepts the values AzureGermanCloud;AzureStackCloud but those are not accepted in CAPZ; an error
// message is returned if those values are used.
func parseCloudType(cloudType string) (string, error) {
	cloudType = strings.ToLower(strings.TrimSpace(cloudType))
	switch cloudType {
	case "azurepubliccloud":
		return "public", nil
	case "azureusgovernmentcloud":
		return "usgovernment", nil
	case "azurechinacloud":
		return "china", nil
	default:
		return "", fmt.Errorf("unsupported cloud type: %s", cloudType)
	}
}

// reconcileKMSConfigSecret reconciles the data needed for the KMS configuration secret.
func reconcileKMSConfigSecret(secret *corev1.Secret, hc *hyperv1.HostedCluster) error {
	azureConfig := azure.AzureConfig{
		Cloud:                        hc.Spec.Platform.Azure.Cloud,
		TenantID:                     hc.Spec.Platform.Azure.TenantID,
		UseManagedIdentityExtension:  false,
		SubscriptionID:               hc.Spec.Platform.Azure.SubscriptionID,
		ResourceGroup:                hc.Spec.Platform.Azure.ResourceGroupName,
		Location:                     hc.Spec.Platform.Azure.Location,
		LoadBalancerName:             hc.Spec.InfraID,
		CloudProviderBackoff:         true,
		CloudProviderBackoffDuration: 6,
		UseInstanceMetadata:          false,
		LoadBalancerSku:              "standard",
		DisableOutboundSNAT:          true,
		AADMSIDataPlaneIdentityPath:  config.ManagedAzureCertificatePath + hc.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName,
	}

	serializedConfig, err := json.MarshalIndent(azureConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cloudconfig: %w", err)
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[azure.CloudConfigKey] = serializedConfig

	return nil
}
