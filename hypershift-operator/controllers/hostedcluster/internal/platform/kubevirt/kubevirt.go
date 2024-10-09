package kubevirt

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	cdicore "kubevirt.io/containerized-data-importer-api/pkg/apis/core"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	hostedClusterAnnotation = "hypershift.openshift.io/cluster"
	imageCAPK               = "registry.ci.openshift.org/ocp/4.18:cluster-api-provider-kubevirt"
)

type Kubevirt struct{}

func (p Kubevirt) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, _ hyperv1.APIEndpoint) (client.Object, error) {
	kubevirtCluster := &capikubevirt.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Spec.InfraID,
		},
	}
	kvPlatform := hcluster.Spec.Platform.Kubevirt
	if kvPlatform != nil && kvPlatform.Credentials != nil {
		var infraClusterSecretRef = &corev1.ObjectReference{
			Name:      hyperv1.KubeVirtInfraCredentialsSecretName,
			Namespace: controlPlaneNamespace,
			Kind:      "Secret",
		}
		kubevirtCluster.Spec.InfraClusterSecretRef = infraClusterSecretRef
	}
	if _, err := createOrUpdate(ctx, c, kubevirtCluster, func() error {
		reconcileKubevirtCluster(kubevirtCluster, hcluster)
		return nil
	}); err != nil {
		return nil, err
	}

	return kubevirtCluster, nil
}

func reconcileKubevirtCluster(kubevirtCluster *capikubevirt.KubevirtCluster, hcluster *hyperv1.HostedCluster) {
	// We only create this resource once and then let CAPI own it
	kubevirtCluster.Annotations = map[string]string{
		hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
		capiv1.ManagedByAnnotation: "external",
	}
	// Set the values for upper level controller
	kubevirtCluster.Status.Ready = true
}

func (p Kubevirt) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := imageCAPK
	if envImage := os.Getenv(images.KubevirtCAPIProviderEnvVar); len(envImage) > 0 {
		providerImage = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIKubeVirtProviderImage]; ok {
		providerImage = override
	}
	defaultMode := int32(0640)
	return &appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](1),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: ptr.To[int64](10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "capi-webhooks-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "capi-webhooks-tls",
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           providerImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("100Mi"),
								corev1.ResourceCPU:    resource.MustParse("10m"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "capi-webhooks-tls",
								ReadOnly:  true,
								MountPath: "/tmp/k8s-webhook-server/serving-certs",
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
						Command: []string{"/manager"},
						Args: []string{
							"--namespace", "$(MY_NAMESPACE)",
							"--v=4",
							"--leader-elect=true",
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "healthz",
								ContainerPort: 9440,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/readyz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func (p Kubevirt) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	// If external infra cluster kubeconfig has been provided, copy the secret from the "clusters" to the hosted control plane namespace
	// with the predictable name "kubevirt-infra-credentials"
	kvPlatform := hcluster.Spec.Platform.Kubevirt
	if kvPlatform == nil || kvPlatform.Credentials == nil {
		return nil
	}

	var sourceSecret corev1.Secret
	secretName := client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret.Name}
	if err := c.Get(ctx, secretName, &sourceSecret); err != nil {
		return fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}
	targetSecret := credentialsSecret(controlPlaneNamespace)
	_, err := createOrUpdate(ctx, c, targetSecret, func() error {
		if targetSecret.Data == nil {
			targetSecret.Data = map[string][]byte{}
		}
		for k, v := range sourceSecret.Data {
			targetSecret.Data[k] = v
		}
		return nil
	})
	return err
}

func (Kubevirt) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Kubevirt) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances", "virtualmachines"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{cdicore.GroupName},
			Resources: []string{"datavolumes"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
}

func credentialsSecret(hcpNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hyperv1.KubeVirtInfraCredentialsSecretName,
			Namespace: hcpNamespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		Type: corev1.SecretTypeOpaque,
	}
}

func (Kubevirt) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}
