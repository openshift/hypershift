package azure

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/secretproviderclass"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
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
)

type Azure struct {
	capiProviderImage string
}

func New(capiProviderImage string) *Azure {
	return &Azure{
		capiProviderImage: capiProviderImage,
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
		return reconcileAzureClusterIdentity(hcluster, azureClusterIdentity, controlPlaneNamespace)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Azure cluster identity: %w", err)
	}

	if _, err := createOrUpdate(ctx, c, azureCluster, func() error {
		return reconcileAzureCluster(azureCluster, hcluster, apiEndpoint, azureClusterIdentity, controlPlaneNamespace)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Azure CAPI cluster: %w", err)
	}

	// Reconcile the SecretProviderClass
	nodepoolMgmtSecretProviderClass := manifests.ManagedAzureSecretProviderClass(config.ManagedAzureNodePoolMgmtSecretProviderClassName, controlPlaneNamespace)
	if _, err := createOrUpdate(ctx, c, nodepoolMgmtSecretProviderClass, func() error {
		hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hcluster.Name)
		if err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
			return err
		}
		secretproviderclass.ReconcileManagedAzureSecretProviderClass(nodepoolMgmtSecretProviderClass, hcp, hcluster.Spec.Platform.Azure.ManagedIdentities.ControlPlane.NodePoolManagement.CertificateName, string(hcluster.Spec.Platform.Azure.ManagedIdentities.ControlPlane.NodePoolManagement.ObjectEncoding))
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile KMS SecretProviderClass: %w", err)
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
	managedAzureKeyVaultManagedIdentityClientID, ok := os.LookupEnv(config.AROHCPKeyVaultManagedIdentityClientID)
	if !ok {
		return nil, fmt.Errorf("environment variable %s is not set", config.AROHCPKeyVaultManagedIdentityClientID)
	}
	return &appsv1.DeploymentSpec{
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
						{
							Name:  config.AROHCPKeyVaultManagedIdentityClientID,
							Value: managedAzureKeyVaultManagedIdentityClientID,
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
						{
							Name:      config.ManagedAzureNodePoolMgmtSecretStoreVolumeName,
							MountPath: config.ManagedAzureCertificateMountPath,
							ReadOnly:  true,
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
					{
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
				},
			}}}, nil
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

func (a Azure) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
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

func reconcileAzureClusterIdentity(hc *hyperv1.HostedCluster, azureClusterIdentity *capiazure.AzureClusterIdentity, controlPlaneNamespace string) error {
	azureClusterIdentity.Spec = capiazure.AzureClusterIdentitySpec{
		ClientID: hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.NodePoolManagement.ClientID,
		TenantID: hc.Spec.Platform.Azure.TenantID,
		CertPath: config.ManagedAzureCertificatePath + hc.Spec.Platform.Azure.ManagedIdentities.ControlPlane.NodePoolManagement.CertificateName,
		Type:     capiazure.ServicePrincipalCertificate,
		AllowedNamespaces: &capiazure.AllowedNamespaces{
			NamespaceList: []string{
				controlPlaneNamespace,
			},
		},
	}
	return nil
}
