package aws

import (
	"context"
	"fmt"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	ImageStreamCAPA        = "aws-cluster-api-controllers"
	awsCredentialsTemplate = `[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
sts_regional_endpoints = regional
region = %s
`
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
	awsCluster := &capiaws.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	var nodePools []hyperv1.NodePool
	if hcluster.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
		// Fetch nodepools to set AWSCluster subnets
		nodePoolList := &hyperv1.NodePoolList{}
		if err := c.List(ctx, nodePoolList, client.InNamespace(hcluster.Namespace)); err != nil {
			return nil, fmt.Errorf("failed to list nodepools: %w", err)
		}
		for i := range nodePoolList.Items {
			if nodePoolList.Items[i].Spec.ClusterName == hcluster.Name {
				nodePools = append(nodePools, nodePoolList.Items[i])
			}
		}
	}

	_, err := createOrUpdate(ctx, c, awsCluster, func() error {
		return reconcileAWSCluster(awsCluster, hcluster, apiEndpoint, nodePools)
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

	featureGates := []string{
		"EKS=false",
	}
	if p.payloadVersion != nil && p.payloadVersion.Major == 4 && p.payloadVersion.Minor > 15 {
		featureGates = append(featureGates, "ROSA=false")
	}

	defaultMode := int32(0640)
	deploymentSpec := &appsv1.DeploymentSpec{
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
							"--v=4",
							"--leader-elect=true",
							fmt.Sprintf("--feature-gates=%s", strings.Join(featureGates, ",")),
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
								corev1.ResourceMemory: resource.MustParse("30Mi"),
							},
						},
					},
				},
			},
		},
	}
	return deploymentSpec, nil
}

func buildAWSWebIdentityCredentials(roleArn, region string) (string, error) {
	if roleArn == "" {
		return "", fmt.Errorf("role arn cannot be empty in AssumeRole credentials")
	}
	if region == "" {
		return "", fmt.Errorf("a region must be specified for cross-partition compatibility in AssumeRole credentials")
	}
	return fmt.Sprintf(awsCredentialsTemplate, roleArn, region), nil
}

func (p AWS) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	// TODO (alberto): consider moving this reconciliation logic down to the CPO.
	// this is not trivial as the CPO deployment itself needs the secret with the ControlPlaneOperatorARN
	var errs []error
	var region string
	if platformSpec := hcluster.Spec.Platform.AWS; platformSpec != nil {
		region = platformSpec.Region
	}
	syncSecret := func(secret *corev1.Secret, arn string) error {
		credentials, err := buildAWSWebIdentityCredentials(arn, region)
		if err != nil {
			return fmt.Errorf("failed to build cloud credentials secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
		if _, err := createOrUpdate(ctx, c, secret, func() error {
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
		if err := syncSecret(secret, arn); err != nil {
			errs = append(errs, err)
		}
	}

	if hcluster.Spec.SecretEncryption != nil && hcluster.Spec.SecretEncryption.KMS != nil && hcluster.Spec.SecretEncryption.KMS.AWS != nil &&
		hcluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN != "" && hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN != "" {
		err := syncSecret(AWSKMSCredsSecret(controlPlaneNamespace), hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (AWS) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func ValidCredentials(hc *hyperv1.HostedCluster) bool {
	oidcConfigValid := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidOIDCConfiguration))
	if oidcConfigValid != nil && oidcConfigValid.Status == metav1.ConditionFalse {
		return false
	}
	validIdentityProvider := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
	if validIdentityProvider != nil && validIdentityProvider.Status != metav1.ConditionTrue {
		return false
	}
	return true
}

func (AWS) DeleteOrphanedMachines(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	if ValidCredentials(hc) {
		return nil
	}
	awsMachineList := capiaws.AWSMachineList{}
	if err := c.List(ctx, &awsMachineList, client.InNamespace(controlPlaneNamespace)); err != nil {
		return fmt.Errorf("failed to list AWSMachines in %s: %w", controlPlaneNamespace, err)
	}
	logger := ctrl.LoggerFrom(ctx)
	var errs []error
	for i := range awsMachineList.Items {
		awsMachine := &awsMachineList.Items[i]
		if !awsMachine.DeletionTimestamp.IsZero() {
			awsMachine.Finalizers = []string{}
			if err := c.Update(ctx, awsMachine); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete machine %s/%s: %w", awsMachine.Namespace, awsMachine.Name, err))
				continue
			}
			logger.Info("skipping cleanup of awsmachine because of invalid AWS identity provider", "machine", client.ObjectKeyFromObject(awsMachine))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func reconcileAWSCluster(awsCluster *capiaws.AWSCluster, hcluster *hyperv1.HostedCluster, apiEndpoint hyperv1.APIEndpoint, nodePools []hyperv1.NodePool) error {
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
			awsCluster.Spec.AdditionalTags = capiaws.Tags{}
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

	if hcluster.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
		subnetIDs := sets.New[string]()
		for i := range nodePools {
			subnetIDPtr := nodePools[i].Spec.Platform.AWS.Subnet.ID
			if subnetIDPtr != nil {
				subnetIDs.Insert(*subnetIDPtr)
			}
		}
		awsCluster.Spec.NetworkSpec.Subnets = nil
		for _, id := range sets.List(subnetIDs) {
			awsCluster.Spec.NetworkSpec.Subnets = append(awsCluster.Spec.NetworkSpec.Subnets, capiaws.SubnetSpec{
				ID:       id,
				IsPublic: true,
			})
		}
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

func AWSKMSCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "kms-creds",
		},
	}
}
