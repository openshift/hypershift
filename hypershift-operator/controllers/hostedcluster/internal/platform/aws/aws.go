package aws

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiawsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Image built from https://github.com/openshift/cluster-api-provider-aws/tree/release-1.1
	// Upstream canonical image comes from  https://console.cloud.google.com/gcr/images/k8s-artifacts-prod
	// us.gcr.io/k8s-artifacts-prod/cluster-api-aws/cluster-api-aws-controller:v1.1.0
	imageCAPA = "registry.ci.openshift.org/hypershift/cluster-api-aws-controller:v1.1.0"
)

type AWS struct{}

func (p AWS) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string,
	apiEndpoint hyperv1.APIEndpoint,
) (client.Object, error) {
	awsCluster := &capiawsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	_, err := createOrUpdate(ctx, c, awsCluster, func() error {
		return reconcileAWSCluster(awsCluster, hcluster, apiEndpoint)
	})
	if err != nil {
		return nil, err
	}
	// reconciliation strips TypeMeta. We repopulate the static values since they are necessary for
	// downstream reconciliation of the CAPI Cluster resource.
	awsCluster.TypeMeta = metav1.TypeMeta{
		Kind:       "AWSCluster",
		APIVersion: capiawsv1.GroupVersion.String(),
	}
	return awsCluster, nil
}

func (p AWS) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error) {
	defaultMode := int32(420)
	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
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
					{
						Name: "credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name,
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
						Name: "token",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								Medium: corev1.StorageMediumMemory,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           imageCAPA,
						ImagePullPolicy: corev1.PullAlways,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "credentials",
								MountPath: "/home/.aws",
							},
							{
								Name:      "capi-webhooks-tls",
								ReadOnly:  true,
								MountPath: "/tmp/k8s-webhook-server/serving-certs",
							},
							{
								Name:      "token",
								MountPath: "/var/run/secrets/openshift/serviceaccount",
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
								Name:  "AWS_SHARED_CREDENTIALS_FILE",
								Value: "/home/.aws/credentials",
							},
							{
								Name:  "AWS_SDK_LOAD_CONFIG",
								Value: "true",
							},
						},
						Command: []string{"/manager"},
						Args: []string{"--namespace", "$(MY_NAMESPACE)",
							"--alsologtostderr",
							"--v=4",
							"--leader-elect=true",
							"--feature-gates=EKS=false",
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
					{
						Name:            "token-minter",
						Image:           tokenMinterImage,
						ImagePullPolicy: corev1.PullAlways,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "token",
								MountPath: "/var/run/secrets/openshift/serviceaccount",
							},
							{
								Name:      "svc-kubeconfig",
								MountPath: "/etc/kubernetes",
							},
						},
						Command: []string{"/usr/bin/token-minter"},
						Args: []string{
							"-service-account-namespace=kube-system",
							"-service-account-name=capa-controller-manager",
							"-token-audience=openshift",
							"-token-file=/var/run/secrets/openshift/serviceaccount/token",
							"-kubeconfig=/etc/kubernetes/kubeconfig",
						},
					},
				},
			},
		},
	}
	return deploymentSpec, nil
}

func (p AWS) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	// Reconcile the platform provider cloud controller credentials secret by resolving
	// the reference from the HostedCluster and syncing the secret in the control
	// plane namespace.
	var src corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.KubeCloudControllerCreds.Name}, &src); err != nil {
		return fmt.Errorf("failed to get cloud controller provider creds %s: %w", hcluster.Spec.Platform.AWS.KubeCloudControllerCreds.Name, err)
	}
	dest := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err := createOrUpdate(ctx, c, dest, func() error {
		srcData, srcHasData := src.Data["credentials"]
		if !srcHasData {
			return fmt.Errorf("hostedcluster cloud controller provider credentials secret %q must have a credentials key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["credentials"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile cloud controller provider creds: %w", err)
	}

	// Reconcile the platform provider node pool management credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get node pool provider creds %s: %w", hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err = createOrUpdate(ctx, c, dest, func() error {
		srcData, srcHasData := src.Data["credentials"]
		if !srcHasData {
			return fmt.Errorf("node pool provider credentials secret %q is missing credentials key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["credentials"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile node pool provider creds: %w", err)
	}

	// Reconcile the platform provider node pool management credentials secret by
	// resolving  the reference from the HostedCluster and syncing the secret in
	// the control plane namespace.
	err = c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.ControlPlaneOperatorCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get control plane operator provider creds %s: %w", hcluster.Spec.Platform.AWS.ControlPlaneOperatorCreds.Name, err)
	}
	dest = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err = createOrUpdate(ctx, c, dest, func() error {
		srcData, srcHasData := src.Data["credentials"]
		if !srcHasData {
			return fmt.Errorf("control plane operator provider credentials secret %q is missing credentials key", src.Name)
		}
		dest.Type = corev1.SecretTypeOpaque
		if dest.Data == nil {
			dest.Data = map[string][]byte{}
		}
		dest.Data["credentials"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile control plane operator provider creds: %w", err)
	}
	return nil
}

func (AWS) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	if hcluster.Spec.SecretEncryption.KMS.AWS == nil || len(hcluster.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name) == 0 {
		return fmt.Errorf("aws kms metadata nil")
	}
	var src corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name}, &src); err != nil {
		return fmt.Errorf("failed to get ibmcloud kms credentials %s: %w", hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Unmanaged.Credentials.Name, err)
	}
	if _, ok := src.Data[hyperv1.AWSCredentialsFileSecretKey]; !ok {
		return fmt.Errorf("aws credential key %s not present in auth secret", hyperv1.AWSCredentialsFileSecretKey)
	}
	hostedControlPlaneAWSKMSAuthSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      src.Name,
		},
	}
	_, err := createOrUpdate(ctx, c, hostedControlPlaneAWSKMSAuthSecret, func() error {
		if hostedControlPlaneAWSKMSAuthSecret.Data == nil {
			hostedControlPlaneAWSKMSAuthSecret.Data = map[string][]byte{}
		}
		hostedControlPlaneAWSKMSAuthSecret.Data[hyperv1.AWSCredentialsFileSecretKey] = src.Data[hyperv1.AWSCredentialsFileSecretKey]
		hostedControlPlaneAWSKMSAuthSecret.Type = corev1.SecretTypeOpaque
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed reconciling aws kms backup key: %w", err)
	}
	return nil
}

func reconcileAWSCluster(awsCluster *capiawsv1.AWSCluster, hcluster *hyperv1.HostedCluster, apiEndpoint hyperv1.APIEndpoint) error {
	// We only create this resource once and then let CAPI own it
	awsCluster.Annotations = map[string]string{
		capiv1.ManagedByAnnotation: "external",
	}

	awsCluster.Spec.AdditionalTags = nil
	if hcluster.Spec.Platform.AWS != nil {
		awsCluster.Spec.Region = hcluster.Spec.Platform.AWS.Region

		if hcluster.Spec.Platform.AWS.CloudProviderConfig != nil {
			awsCluster.Spec.NetworkSpec.VPC.ID = hcluster.Spec.Platform.AWS.CloudProviderConfig.VPC

			// TODO: This https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2728
			// broke our assumption in CAPA 0.7 for externally managed infrastructure.
			// This effectively limit our ability to span NodePools across multiple subnets.
			// In a follow up we need to either enable upstream back to support arbitrary subnets IDs
			// in the awsMachine CR or possibly expose a slice of available subnets for NodePools in hcluster.Spec.Platform.AWS.
			if hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet != nil &&
				hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
				awsCluster.Spec.NetworkSpec.Subnets = []capiawsv1.SubnetSpec{
					{
						ID: *hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID,
					},
				}
			}
		}

		if len(hcluster.Spec.Platform.AWS.ResourceTags) > 0 {
			awsCluster.Spec.AdditionalTags = capiawsv1.Tags{}
		}
		for _, entry := range hcluster.Spec.Platform.AWS.ResourceTags {
			awsCluster.Spec.AdditionalTags[entry.Key] = entry.Value
		}
	}

	// Set the values for upper level controller
	awsCluster.Status.Ready = true
	awsCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}
	return nil
}

func (AWS) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}
