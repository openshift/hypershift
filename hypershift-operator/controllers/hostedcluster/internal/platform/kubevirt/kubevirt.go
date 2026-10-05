package kubevirt

import (
	"context"
	"errors"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	cdicore "kubevirt.io/containerized-data-importer-api/pkg/apis/core"
)

const (
	hostedClusterAnnotation = "hypershift.openshift.io/cluster"
)

type Kubevirt struct {
	payloadVersion *semver.Version
}

func New(payloadVersion *semver.Version) *Kubevirt {
	return &Kubevirt{
		payloadVersion: payloadVersion,
	}
}

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

func (p Kubevirt) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := ""
	if envImage := os.Getenv(images.KubevirtCAPIProviderEnvVar); len(envImage) > 0 {
		providerImage = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIKubeVirtProviderImage]; ok {
		providerImage = override
	}
	if providerImage == "" {
		return nil, fmt.Errorf("kubevirt CAPI provider image not specified by environment variable %s or annotation %s", images.KubevirtCAPIProviderEnvVar, hyperv1.ClusterAPIKubeVirtProviderImage)
	}

	args := []string{
		"--namespace", "$(MY_NAMESPACE)",
		"--v=4",
		"--leader-elect=true",
	}
	if hcp != nil && p.payloadVersion != nil && (p.payloadVersion.Major >= 5 || (p.payloadVersion.Major == 4 && p.payloadVersion.Minor >= 23)) {
		tlsArgs, err := config.TLSArgs(hcp.Spec.Configuration.GetTLSSecurityProfile())
		if err != nil {
			return nil, err
		}
		if len(tlsArgs) > 0 {
			args = append(args, tlsArgs...)
		}
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
						Args:    args,
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
	data, err := p.ValidateCredentials(ctx, c, hcluster, controlPlaneNamespace)
	if err != nil || data == nil {
		return err
	}
	targetSecret := credentialsSecret(controlPlaneNamespace)
	_, err = createOrUpdate(ctx, c, targetSecret, func() error {
		targetSecret.Data = data
		return nil
	})
	return err
}

// ValidateCredentials checks tenant credentials before either publication or
// infrastructure discovery, removing a previously copied unsafe target on rejection.
func (p Kubevirt) ValidateCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) (map[string][]byte, error) {
	kvPlatform := hcluster.Spec.Platform.Kubevirt
	if kvPlatform == nil || kvPlatform.Credentials == nil {
		return nil, p.deleteUnsafeCredentials(ctx, c, controlPlaneNamespace)
	}
	ref := kvPlatform.Credentials.InfraKubeConfigSecret
	if ref == nil {
		return nil, p.rejectCredentials(ctx, c, controlPlaneNamespace, errors.New("infrastructure credential reference is missing"))
	}
	if ref.Name == "" {
		return nil, p.rejectCredentials(ctx, c, controlPlaneNamespace, errors.New("infrastructure credential name is empty"))
	}
	if ref.Key == "" {
		return nil, p.rejectCredentials(ctx, c, controlPlaneNamespace, errors.New("infrastructure credential key is empty"))
	}
	var sourceSecret corev1.Secret
	secretName := client.ObjectKey{Namespace: hcluster.Namespace, Name: ref.Name}
	if err := c.Get(ctx, secretName, &sourceSecret); err != nil {
		return nil, p.rejectCredentials(ctx, c, controlPlaneNamespace, fmt.Errorf("failed to get secret %s: %w", secretName, err))
	}
	data, err := kvinfra.KubeConfigData(&sourceSecret, ref.Key)
	if err != nil {
		return nil, p.rejectCredentials(ctx, c, controlPlaneNamespace, err)
	}
	// An older operator may have copied unsafe credentials before the source was
	// corrected. Remediate that target even if later prerequisites stop publication.
	if err := p.deleteUnsafeCredentials(ctx, c, controlPlaneNamespace); err != nil {
		return nil, err
	}
	return data, nil
}

func (p Kubevirt) rejectCredentials(ctx context.Context, c client.Client, controlPlaneNamespace string, credentialErr error) error {
	return errors.Join(credentialErr, p.deleteUnsafeCredentials(ctx, c, controlPlaneNamespace))
}

func (Kubevirt) deleteUnsafeCredentials(ctx context.Context, c client.Client, controlPlaneNamespace string) error {
	targetSecret := credentialsSecret(controlPlaneNamespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(targetSecret), targetSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect infrastructure credential target: %w", err)
	}
	// Stored targets must actually have the canonical key: source normalization
	// must not synthesize an alias and certify unusable persisted credentials.
	safe := kvinfra.ValidateKubeConfig(targetSecret.Data["kubeconfig"]) == nil
	if safe {
		// Older operators copied every source entry, including previous custom keys.
		// Only namespace is metadata; audit all other persisted data as credentials.
		for key, data := range targetSecret.Data {
			if key != "namespace" && kvinfra.ValidateKubeConfig(data) != nil {
				safe = false
				break
			}
		}
	}
	if safe {
		// Keep last-known-safe credentials when a tenant submits an invalid replacement.
		return nil
	}
	if err := c.Delete(ctx, targetSecret, &client.DeleteOptions{Preconditions: &metav1.Preconditions{
		UID: &targetSecret.UID, ResourceVersion: &targetSecret.ResourceVersion,
	}}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to remove unsafe infrastructure credential target: %w", err)
	}
	return nil
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

func (p Kubevirt) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	var ref *hyperv1.KubeconfigSecretRef
	if kv := hcluster.Spec.Platform.Kubevirt; kv != nil && kv.Credentials != nil && kv.Credentials.InfraKubeConfigSecret != nil {
		ref = kv.Credentials.InfraKubeConfigSecret
	}
	if err := p.deleteUnsafeCredentials(ctx, c, controlPlaneNamespace); err != nil {
		return err
	}
	if ref == nil || ref.Name == "" || ref.Key == "" {
		return nil
	}
	var source corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: ref.Name}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect infrastructure source during deletion: %w", err)
	}
	// Recovery publication is eligible only for validated source credentials.
	// Rejected input still permits teardown that does not consume an infra client.
	_, validationErr := kvinfra.KubeConfigData(&source, ref.Key)
	if validationErr == nil {
		var namespace corev1.Namespace
		if err := c.Get(ctx, client.ObjectKey{Name: controlPlaneNamespace}, &namespace); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to inspect control plane namespace during deletion: %w", err)
		}
		if !namespace.DeletionTimestamp.IsZero() {
			return nil
		}
		// CAPK needs the fixed target until Machines/VMs finish deleting. Reuse normal
		// validated publication, without recreating deleted/terminating namespaces.
		return p.ReconcileCredentials(ctx, c, upsert.New(false).CreateOrUpdate, hcluster, controlPlaneNamespace)
	}
	return nil
}
