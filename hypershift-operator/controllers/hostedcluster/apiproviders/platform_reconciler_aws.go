package apiproviders

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiawsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4"
	capiv1alpha4 "sigs.k8s.io/cluster-api/api/v1alpha4"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	// TODO (alberto): Eventually this image will be mirrored and pulled from an internal registry.
	// This comes from https://console.cloud.google.com/gcr/images/k8s-artifacts-prod
	imageCAPA = "us.gcr.io/k8s-artifacts-prod/cluster-api-aws/cluster-api-aws-controller:v0.7.0"
)

type AWSPlatformReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
}

func NewAWSPlatformReconciler(cl client.Client, cuProv upsert.CreateOrUpdateProvider) *AWSPlatformReconciler {
	return &AWSPlatformReconciler{
		Client:                 cl,
		CreateOrUpdateProvider: cuProv,
	}
}

func (r AWSPlatformReconciler) ReconclieCred(ctx context.Context, hcluster *hyperv1.HostedCluster, cpns string) error {
	var src corev1.Secret
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.KubeCloudControllerCreds.Name}, &src)
	if err != nil {
		return err
	}
	dest := manifests.AWSKubeCloudControllerCreds(cpns)
	_, err = r.CreateOrUpdate(ctx, r.Client, dest, func() error {
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
	return err
}

func (r AWSPlatformReconciler) ReconcileSecret(ctx context.Context, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	var src corev1.Secret
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get node pool provider creds %s: %w", hcluster.Spec.Platform.AWS.NodePoolManagementCreds.Name, err)
	}

	dest := manifests.AWSNodePoolManagementCreds(controlPlaneNamespace)
	_, err = r.CreateOrUpdate(ctx, r.Client, dest, func() error {
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

	return nil
}

func (r AWSPlatformReconciler) GetInfraCR(ctx context.Context, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, controlPlaneNamespace string) (client.Object, error) {
	// Reconcile external AWSCluster
	awsCluster := controlplaneoperator.AWSCluster(controlPlaneNamespace, hcluster.Name)
	_, err := controllerutil.CreateOrPatch(ctx, r.Client, awsCluster, func() error {
		return reconcileAWSCluster(awsCluster, hcluster, hcp.Status.ControlPlaneEndpoint)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile AWSCluster: %w", err)
	}

	return awsCluster, nil
}

// ReconcileCAPIProvider orchestrates reconciliation of the CAPI AWS provider
// components.
func (r *AWSPlatformReconciler) ReconcileCAPIProvider(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI AWS provider role
	capiAwsProviderRole := clusterapi.CAPIAWSProviderRole(controlPlaneNamespace.Name)
	_, err = r.CreateOrUpdate(ctx, r.Client, capiAwsProviderRole, func() error {
		return reconcileCAPIAWSProviderRole(capiAwsProviderRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role: %w", err)
	}

	// Reconcile CAPI AWS provider service account
	capiAwsProviderServiceAccount := clusterapi.CAPIAWSProviderServiceAccount(controlPlaneNamespace.Name)
	_, err = r.CreateOrUpdate(ctx, r.Client, capiAwsProviderServiceAccount, hyperutil.NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider service account: %w", err)
	}

	// Reconcile CAPI AWS provider role binding
	capiAwsProviderRoleBinding := clusterapi.CAPIAWSProviderRoleBinding(controlPlaneNamespace.Name)
	_, err = r.CreateOrUpdate(ctx, r.Client, capiAwsProviderRoleBinding, func() error {
		return reconcileCAPIAWSProviderRoleBinding(capiAwsProviderRoleBinding, capiAwsProviderRole, capiAwsProviderServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider role binding: %w", err)
	}

	// Reconcile CAPI AWS provider deployment
	capiAwsProviderDeployment := clusterapi.CAPIAWSProviderDeployment(controlPlaneNamespace.Name)
	_, err = r.CreateOrUpdate(ctx, r.Client, capiAwsProviderDeployment, func() error {
		// TODO (alberto): This image builds from https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2453
		// We need to build from main branch and push to quay.io/hypershift once this is merged or otherwise enable webhooks.
		return reconcileCAPIAWSProviderDeployment(capiAwsProviderDeployment, hcluster, capiAwsProviderServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi aws provider deployment: %w", err)
	}

	return nil
}

func reconcileAWSCluster(awsCluster *capiawsv1.AWSCluster, hcluster *hyperv1.HostedCluster, apiEndpoint hyperv1.APIEndpoint) error {
	// We only create this resource once and then let CAPI own it
	awsCluster.Annotations = map[string]string{
		HostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
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
	awsCluster.Spec.ControlPlaneEndpoint = capiv1alpha4.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}
	return nil
}

func reconcileCAPIAWSProviderDeployment(deployment *appsv1.Deployment, hc *hyperv1.HostedCluster, sa *corev1.ServiceAccount) error {
	defaultMode := int32(420)
	capaLabels := map[string]string{
		"control-plane":               "capa-controller-manager",
		"app":                         "capa-controller-manager",
		hyperv1.ControlPlaneComponent: "capa-controller-manager",
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: capaLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: capaLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
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
								SecretName: manifests.AWSNodePoolManagementCreds(deployment.Namespace).Name,
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
							Handler: corev1.Handler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
						ReadinessProbe: &corev1.Probe{
							Handler: corev1.Handler{
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
	hyperutil.SetColocation(hc, deployment)
	// TODO (alberto): Reconsider enable this back when we face a real need
	// with no better solution.
	// hyperutil.SetRestartAnnotation(hc, deployment)
	hyperutil.SetControlPlaneIsolation(hc, deployment)
	hyperutil.SetDefaultPriorityClass(deployment)
	switch hc.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		maxSurge := intstr.FromInt(1)
		maxUnavailable := intstr.FromInt(1)
		deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
		deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
			MaxSurge:       &maxSurge,
			MaxUnavailable: &maxUnavailable,
		}
		deployment.Spec.Replicas = k8sutilspointer.Int32Ptr(3)
		hyperutil.SetMultizoneSpread(capaLabels, deployment)
	default:
		deployment.Spec.Replicas = k8sutilspointer.Int32Ptr(1)
	}

	return nil
}

func reconcileCAPIAWSProviderRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"secrets",
				"configmaps",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
	}
	return nil
}

func reconcileCAPIAWSProviderRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}
