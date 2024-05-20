package openstack

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OpenStack struct {
	capiProviderImage string
}

func New(capiProviderImage string) *OpenStack {
	return &OpenStack{
		capiProviderImage: capiProviderImage,
	}
}

func (a OpenStack) ReconcileCAPIInfraCR(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {
	return nil, nil
}

func (a OpenStack) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	// image := a.capiProviderImage
	// if envImage := os.Getenv(images.AzureCAPIProviderEnvVar); len(envImage) > 0 {
	// 	image = envImage
	// }
	// if override, ok := hcluster.Annotations[hyperv1.ClusterAPIAzureProviderImage]; ok {
	// 	image = override
	// }
	// defaultMode := int32(0640)
	// return &appsv1.DeploymentSpec{
	// 	Template: corev1.PodTemplateSpec{
	// 		Spec: corev1.PodSpec{
	// 			TerminationGracePeriodSeconds: k8sutilspointer.Int64(10),
	// 			Containers: []corev1.Container{{
	// 				Name:            "manager",
	// 				Image:           image,
	// 				ImagePullPolicy: corev1.PullIfNotPresent,
	// 				Args: []string{
	// 					"--namespace=$(MY_NAMESPACE)",
	// 					"--leader-elect=true",
	// 				},
	// 				Resources: corev1.ResourceRequirements{
	// 					Requests: corev1.ResourceList{
	// 						corev1.ResourceCPU:    resource.MustParse("10m"),
	// 						corev1.ResourceMemory: resource.MustParse("10Mi"),
	// 					},
	// 				},
	// 				Env: []corev1.EnvVar{
	// 					{
	// 						Name: "MY_NAMESPACE",
	// 						ValueFrom: &corev1.EnvVarSource{
	// 							FieldRef: &corev1.ObjectFieldSelector{
	// 								FieldPath: "metadata.namespace",
	// 							},
	// 						},
	// 					},
	// 				},
	// 				VolumeMounts: []corev1.VolumeMount{
	// 					{
	// 						Name:      "capi-webhooks-tls",
	// 						ReadOnly:  true,
	// 						MountPath: "/tmp/k8s-webhook-server/serving-certs",
	// 					},
	// 					{
	// 						Name:      "svc-kubeconfig",
	// 						MountPath: "/etc/kubernetes",
	// 					},
	// 				},
	// 			}},
	// 			Volumes: []corev1.Volume{
	// 				{
	// 					Name: "capi-webhooks-tls",
	// 					VolumeSource: corev1.VolumeSource{
	// 						Secret: &corev1.SecretVolumeSource{
	// 							SecretName: "capi-webhooks-tls",
	// 						},
	// 					},
	// 				},
	// 				{
	// 					Name: "svc-kubeconfig",
	// 					VolumeSource: corev1.VolumeSource{
	// 						Secret: &corev1.SecretVolumeSource{
	// 							DefaultMode: &defaultMode,
	// 							SecretName:  "service-network-admin-kubeconfig",
	// 						},
	// 					},
	// 				},
	// 			},
	// 		}}}, nil
	return nil, nil
}

func (a OpenStack) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return errors.Join(
		a.reconcileCloudsYaml(ctx, c, createOrUpdate, controlPlaneNamespace, hcluster.Namespace, hcluster.Spec.Platform.OpenStack.CloudsYamlSecret),
		a.reconcileCACert(ctx, c, createOrUpdate, controlPlaneNamespace, hcluster.Namespace, hcluster.Spec.Platform.OpenStack.CACertSecret),
	)
}

func (a OpenStack) reconcileCloudsYaml(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, controlPlaneNamespace string, clusterNamespace string, cloudsYamlSecret corev1.LocalObjectReference) error {
	var source corev1.Secret

	// Sync user cloud.conf secret
	name := client.ObjectKey{Namespace: clusterNamespace, Name: cloudsYamlSecret.Name}
	if err := c.Get(ctx, name, &source); err != nil {
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	clouds := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: cloudsYamlSecret.Name}}
	_, err := createOrUpdate(ctx, c, clouds, func() error {
		if clouds.Data == nil {
			clouds.Data = map[string][]byte{}
		}
		clouds.Data["clouds.yaml"] = source.Data["clouds.yaml"] // TODO(dulek): Proper missing key handling.
		clouds.Data["clouds.conf"] = source.Data["clouds.conf"] // TODO(dulek): Could we just generate this from clouds.yaml here?
		return nil
	})

	return err
}

func (a OpenStack) reconcileCACert(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, controlPlaneNamespace string, clusterNamespace string, caCertSecret *corev1.LocalObjectReference) error {
	if caCertSecret == nil {
		return nil
	}

	var source corev1.Secret

	// TODO(dulek): Switch this to a ConfigMap
	name := client.ObjectKey{Namespace: clusterNamespace, Name: caCertSecret.Name}
	if err := c.Get(ctx, name, &source); err != nil {
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	caCert := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "openstack-ca"}}
	if _, err := createOrUpdate(ctx, c, caCert, func() error {
		if caCert.Data == nil {
			caCert.Data = map[string][]byte{}
		}
		caCert.Data["ca.pem"] = source.Data["ca.pem"] // TODO(dulek): Proper missing key handling, naming.
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (a OpenStack) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func (a OpenStack) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}

func (a OpenStack) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	// for _, name := range []string{"cloud-ca", "cloud-provider-config", "openstack-cloud-credentials"} {
	// 	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: name}}
	// 	err := c.Delete(ctx, secret)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to delete secret %s: %w", name, err)
	// 	}
	// }
	return nil
}
