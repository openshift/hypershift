package azure

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const providerImage = "gcr.io/k8s-staging-cluster-api-azure/cluster-api-azure-controller:v20220217-v1.1.0-193-gf7fd1995"

type Azure struct{}

func (a *Azure) ReconcileCAPIInfraCR(
	ctx context.Context,
	client client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string,
	apiEndpoint hyperv1.APIEndpoint,
) (client.Object, error) {

	cluster := &capiazure.AzureCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hcluster.Spec.InfraID,
			Namespace: controlPlaneNamespace,
		},
	}
	if _, err := createOrUpdate(ctx, client, cluster, func() error {
		if cluster.Annotations == nil {
			cluster.Annotations = map[string]string{}
		}
		cluster.Annotations[capiv1.ManagedByAnnotation] = "external"

		cluster.Spec.Location = hcluster.Spec.Platform.Azure.Location
		cluster.Spec.ResourceGroup = hcluster.Spec.Platform.Azure.ResourceGroupName
		cluster.Spec.NetworkSpec.Vnet.ID = hcluster.Spec.Platform.Azure.VnetID
		cluster.Spec.NetworkSpec.Vnet.Name = hcluster.Spec.Platform.Azure.VnetName
		cluster.Spec.NetworkSpec.Vnet.ResourceGroup = hcluster.Spec.Platform.Azure.ResourceGroupName
		cluster.Spec.SubscriptionID = hcluster.Spec.Platform.Azure.SubscriptionID

		cluster.Status.Ready = true
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to upsert Azure capi cluster: %w", err)
	}

	return cluster, nil
}

func (a *Azure) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	image := providerImage
	if envImage := os.Getenv(images.AzureCAPIProviderEnvVar); len(envImage) > 0 {
		image = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIAzureProviderImage]; ok {
		image = override
	}
	return &appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:    "manager",
			Image:   image,
			Command: []string{"/manager"},
			Args: []string{
				"--namespace=$(MY_NAMESPACE)",
				"--leader-elect=true",
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
					Name: "AZURE_SUBSCRIPTION_ID",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: hcluster.Spec.Platform.Azure.Credentials.Name},
						Key:                  "AZURE_SUBSCRIPTION_ID",
					}},
				},
				{
					Name: "AZURE_TENANT_ID",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: hcluster.Spec.Platform.Azure.Credentials.Name},
						Key:                  "AZURE_TENANT_ID",
					}},
				},
				{
					Name: "AZURE_CLIENT_ID",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: hcluster.Spec.Platform.Azure.Credentials.Name},
						Key:                  "AZURE_CLIENT_ID",
					}},
				},
				{
					Name: "AZURE_CLIENT_SECRET",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: hcluster.Spec.Platform.Azure.Credentials.Name},
						Key:                  "AZURE_CLIENT_SECRET",
					}},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "capi-webhooks-tls",
					ReadOnly:  true,
					MountPath: "/tmp/k8s-webhook-server/serving-certs",
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
		},
	}}}, nil
}

func (a *Azure) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {

	var source corev1.Secret
	name := client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.Platform.Azure.Credentials.Name}
	if err := c.Get(ctx, name, &source); err != nil {
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: name.Name}}
	_, err := createOrUpdate(ctx, c, target, func() error {
		if target.Data == nil {
			target.Data = map[string][]byte{}
		}
		for k, v := range source.Data {
			target.Data[k] = v
		}
		return nil
	})
	return err
}

func (a *Azure) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func (a *Azure) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}

func (a *Azure) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}
