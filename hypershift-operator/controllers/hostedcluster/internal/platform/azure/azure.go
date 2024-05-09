package azure

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sutilspointer "k8s.io/utils/pointer"
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
	client client.Client,
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

	if _, err := createOrUpdate(ctx, client, azureClusterIdentity, func() error {
		return reconcileAzureClusterIdentity(hcluster, azureClusterIdentity, controlPlaneNamespace)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Azure cluster identity: %w", err)
	}

	if _, err := createOrUpdate(ctx, client, azureCluster, func() error {
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
	return &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: k8sutilspointer.Int64(10),
				Containers: []corev1.Container{{
					Name:            "manager",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args: []string{
						"--namespace=$(MY_NAMESPACE)",
						"--leader-elect=true",
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
							ReadOnly:  true,
							MountPath: "/tmp/k8s-webhook-server/serving-certs",
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
			}}}, nil
}

func (a Azure) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	secretData := resources.AzureFederatedTokenCredentialSecretData(hcluster.ObjectMeta.Name, hcluster.Spec.InfraID, hcluster.Spec.Platform.Azure)
	for clientId, secret := range map[string]*corev1.Secret{
		hcluster.Spec.Platform.Azure.KubeCloudControllerClientID:  manifests.KubeCloudControllerCredsSecret(hcluster.Namespace),
		hcluster.Spec.Platform.Azure.NodePoolManagementClientID:   manifests.NodePoolManagementCredsSecret(hcluster.Namespace), // TODO: not sure we need it, https://capz.sigs.k8s.io/topics/workload-identity does not have CAPZ using a secret
		hcluster.Spec.Platform.Azure.ControlPlaneOperatorClientID: manifests.ControlPlaneOperatorCredsSecret(hcluster.Namespace),
		hcluster.Spec.Platform.Azure.NetworkClientID:              manifests.CloudNetworkConfigControllerCredsSecret(hcluster.Namespace),
		// TODO: aws puts storage creds, do we need to?
	} {
		if _, err := createOrUpdate(ctx, c, secret, func() error {
			secret.Data = secretData
			secret.Data["azure_client_id"] = []byte(clientId)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile guest cluster secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
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

func reconcileAzureClusterIdentity(hcluster *hyperv1.HostedCluster, azureClusterIdentity *capiazure.AzureClusterIdentity, controlPlaneNamespace string) error {
	azureClusterIdentity.Spec = capiazure.AzureClusterIdentitySpec{
		ClientID: hcluster.Spec.Platform.Azure.NodePoolManagementClientID,
		TenantID: hcluster.Spec.Platform.Azure.TenantID,
		Type:     capiazure.WorkloadIdentity,
		AllowedNamespaces: &capiazure.AllowedNamespaces{
			NamespaceList: []string{
				controlPlaneNamespace,
			},
		},
	}
	return nil
}
