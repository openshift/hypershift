package aws

import (
	"context"
	"fmt"
	"os"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiawsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ImageStreamCAPA = "aws-cluster-api-controllers"
)

func New(utilitiesImage string, capiProviderImage string, payloadVersion *semver.Version) *AWS {
	return &AWS{
		utilitiesImage:    utilitiesImage,
		capiProviderImage: capiProviderImage,
		payloadVersion:    payloadVersion,
	}
}

type AWS struct {
	utilitiesImage    string
	capiProviderImage string
	payloadVersion    *semver.Version
}

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
	return awsCluster, nil
}

func (p AWS) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := p.capiProviderImage
	if envImage := os.Getenv(images.AWSCAPIProviderEnvVar); len(envImage) > 0 {
		// Only override CAPA image with env var if payload version < 4.12
		if p.payloadVersion != nil && p.payloadVersion.Major == 4 && p.payloadVersion.Minor < 12 {
			providerImage = envImage
		}
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIProviderAWSImage]; ok {
		providerImage = override
	}
	defaultMode := int32(416)
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
								SecretName: NodePoolManagementCredsSecret("").Name,
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
						Image:           p.utilitiesImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
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
						Command: []string{"/usr/bin/control-plane-operator", "token-minter"},
						Args: []string{
							"--service-account-namespace=kube-system",
							"--service-account-name=capa-controller-manager",
							"--token-audience=openshift",
							"--token-file=/var/run/secrets/openshift/serviceaccount/token",
							"--kubeconfig=/etc/kubernetes/kubeconfig",
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("10Mi"),
							},
						},
					},
				},
			},
		},
	}
	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Namespace, util.APIPort(hcp)), p.utilitiesImage, &deploymentSpec.Template.Spec)
	return deploymentSpec, nil
}

func (p AWS) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	awsCredentialsTemplate := `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`
	// TODO (alberto): consider moving this reconciliation logic down to the CPO.
	// this is not trivial as the CPO deployment itself needs the secret with the ControlPlaneOperatorARN
	var errs []error
	syncSecret := func(secret *corev1.Secret, arn string) error {
		if arn == "" {
			return fmt.Errorf("ARN is not provided for cloud credential secret %s/%s", secret.Namespace, secret.Name)
		}
		if _, err := createOrUpdate(ctx, c, secret, func() error {
			credentials := fmt.Sprintf(awsCredentialsTemplate, arn)
			secret.Data = map[string][]byte{"credentials": []byte(credentials)}
			secret.Type = corev1.SecretTypeOpaque
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile aws cloud credential secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
		return nil
	}
	for arn, secret := range map[string]*corev1.Secret{
		hcluster.Spec.Platform.AWS.RolesRef.KubeCloudControllerARN:  KubeCloudControllerCredsSecret(controlPlaneNamespace),
		hcluster.Spec.Platform.AWS.RolesRef.NodePoolManagementARN:   NodePoolManagementCredsSecret(controlPlaneNamespace),
		hcluster.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN: ControlPlaneOperatorCredsSecret(controlPlaneNamespace),
		hcluster.Spec.Platform.AWS.RolesRef.NetworkARN:              CloudNetworkConfigControllerCredsSecret(controlPlaneNamespace),
		hcluster.Spec.Platform.AWS.RolesRef.StorageARN:              AWSEBSCSIDriverCredsSecret(controlPlaneNamespace),
	} {
		err := syncSecret(secret, arn)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
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

func (AWS) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}

func KubeCloudControllerCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cloud-controller-creds",
		},
	}
}

func NodePoolManagementCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "node-management-creds",
		},
	}
}

func ControlPlaneOperatorCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "control-plane-operator-creds",
		},
	}
}

func CloudNetworkConfigControllerCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cloud-network-config-controller-creds",
		},
	}
}

func AWSEBSCSIDriverCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "ebs-cloud-credentials",
		},
	}
}
