package powervs

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

	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func New(capiProviderImage string) *PowerVS {
	return &PowerVS{
		capiProviderImage: capiProviderImage,
	}
}

type PowerVS struct {
	capiProviderImage string
}

func (p PowerVS) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	//TODO(mkumatag): implement me
	return nil
}

func (p PowerVS) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string,
	apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {
	ibmCluster := &capiibmv1.IBMPowerVSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	ibmCluster.Spec.ServiceInstanceID = hcluster.Spec.Platform.PowerVS.ServiceInstanceID

	_, err := createOrUpdate(ctx, c, ibmCluster, func() error {
		ibmCluster.Annotations = map[string]string{
			capiv1.ManagedByAnnotation: "external",
		}

		// Set the values for upper level controller
		ibmCluster.Status.Ready = true
		ibmCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
			Host: apiEndpoint.Host,
			Port: apiEndpoint.Port,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// reconciliation strips TypeMeta. We repopulate the static values since they are necessary for
	// downstream reconciliation of the CAPI Cluster resource.
	ibmCluster.TypeMeta = metav1.TypeMeta{
		Kind:       "IBMPowerVSCluster",
		APIVersion: capiibmv1.GroupVersion.String(),
	}
	return ibmCluster, nil
}

func (p PowerVS) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	defaultMode := int32(416)

	providerImage := p.capiProviderImage
	if envImage := os.Getenv(images.PowerVSCAPIProviderEnvVar); len(envImage) > 0 {
		providerImage = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIPowerVSProviderImage]; ok {
		providerImage = override
	}

	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: ptr.To[int64](10),
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
					{
						Name: "credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: hcluster.Spec.Platform.PowerVS.NodePoolManagementCreds.Name,
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
								Name:      "credentials",
								MountPath: "/home/.ibmcloud",
							},
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
							{
								Name:  "IBM_CREDENTIALS_FILE",
								Value: "/home/.ibmcloud/ibm-credentials.env",
							},
						},
						Command: []string{"/bin/cluster-api-provider-ibmcloud-controller-manager"},
						Args: []string{"--namespace", "$(MY_NAMESPACE)",
							"--v=4",
							"--leader-elect=true",
							"--provider-id-fmt=v2",
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
	}
	return deploymentSpec, nil
}

func (p PowerVS) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	// Reconcile the platform provider cloud controller credentials secret by resolving
	// the reference from the HostedCluster and syncing the secret in the control
	// plane namespace.

	// This secret hosts the IBM Cloud credential with filename ibm-credentials.env
	// used by the different components in the cluster like CAPI controller.
	// The filename will be searched for within the program's working directory, and then the OS's
	// current user directory. This filename can be overridden with the help of IBM_CREDENTIALS_FILE environment variable
	var src corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name}, &src); err != nil {
		return fmt.Errorf("failed to get cloud controller provider creds %s: %w", hcluster.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name, err)
	}
	dest := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err := createOrUpdate(ctx, c, dest, func() error {
		apiKeySrcData, apiKeySrcHasData := src.Data["ibmcloud_api_key"]
		if !apiKeySrcHasData {
			return fmt.Errorf("hostedcluster cloud controller provider credentials secret %q must have a credentials key ibmcloud_api_key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["ibmcloud_api_key"] = apiKeySrcData

		envSrcData, envSrcHasData := src.Data["ibm-credentials.env"]
		if !envSrcHasData {
			return fmt.Errorf("hostedcluster cloud controller provider credentials secret %q must have a credentials key ibm-credentials.env", src.Name)
		}
		dest.Data["ibm-credentials.env"] = envSrcData

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile cloud controller provider creds: %w", err)
	}

	// Reconcile the platform provider node pool management credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.PowerVS.NodePoolManagementCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get node pool provider creds %s: %w", hcluster.Spec.Platform.PowerVS.NodePoolManagementCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err = createOrUpdate(ctx, c, dest, func() error {
		srcData, srcHasData := src.Data["ibm-credentials.env"]
		if !srcHasData {
			return fmt.Errorf("node pool provider credentials secret %q is missing credentials key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["ibm-credentials.env"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile node pool provider creds: %w", err)
	}

	// Reconcile the platform provider ingress operator credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.PowerVS.IngressOperatorCloudCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get ingress operator provider creds %s: %w", hcluster.Spec.Platform.PowerVS.IngressOperatorCloudCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err = createOrUpdate(ctx, c, dest, func() error {
		apiKeySrcData, apiKeySrcHasData := src.Data["ibmcloud_api_key"]
		if !apiKeySrcHasData {
			return fmt.Errorf("hostedcluster ingress operator credentials secret %q must have a credentials key ibmcloud_api_key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["ibmcloud_api_key"] = apiKeySrcData

		envSrcData, envSrcHasData := src.Data["ibm-credentials.env"]
		if !envSrcHasData {
			return fmt.Errorf("hostedcluster ingress operator credentials secret %q must have a credentials key ibm-credentials.env", src.Name)
		}
		dest.Data["ibm-credentials.env"] = envSrcData

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile ingress operator provider creds: %w", err)
	}

	// Reconcile the platform provider storage operator credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.PowerVS.StorageOperatorCloudCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get storage operator provider creds %s: %w", hcluster.Spec.Platform.PowerVS.StorageOperatorCloudCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err = createOrUpdate(ctx, c, dest, func() error {
		apiKeySrcData, apiKeySrcHasData := src.Data["ibmcloud_api_key"]
		if !apiKeySrcHasData {
			return fmt.Errorf("hostedcluster storage operator credentials secret %q must have a credentials key ibmcloud_api_key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["ibmcloud_api_key"] = apiKeySrcData

		envSrcData, envSrcHasData := src.Data["ibm-credentials.env"]
		if !envSrcHasData {
			return fmt.Errorf("hostedcluster storage operator credentials secret %q must have a credentials key ibm-credentials.env", src.Name)
		}
		dest.Data["ibm-credentials.env"] = envSrcData

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile storage operator provider creds: %w", err)
	}

	// Reconcile the platform provider image registry operator credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	if err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.PowerVS.ImageRegistryOperatorCloudCreds.Name}, &src); err != nil {
		return fmt.Errorf("failed to get image registry operator provider creds %s: %w", hcluster.Spec.Platform.PowerVS.ImageRegistryOperatorCloudCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	if _, err = createOrUpdate(ctx, c, dest, func() error {
		apiKeySrcData, apiKeySrcHasData := src.Data["ibmcloud_api_key"]
		if !apiKeySrcHasData {
			return fmt.Errorf("hostedcluster image registry operator credentials secret %q must have a credentials key ibmcloud_api_key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["ibmcloud_api_key"] = apiKeySrcData

		envSrcData, envSrcHasData := src.Data["ibm-credentials.env"]
		if !envSrcHasData {
			return fmt.Errorf("hostedcluster image registry operator credentials secret %q must have a credentials key ibm-credentials.env", src.Name)
		}
		dest.Data["ibm-credentials.env"] = envSrcData

		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator provider creds: %w", err)
	}
	return nil
}

func (PowerVS) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (PowerVS) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}
