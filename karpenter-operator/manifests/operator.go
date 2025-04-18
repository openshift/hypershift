package manifests

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/ptr"

	client "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	name = "karpenter-operator"
)

func KarpenterOperatorDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func KarpenterOperatorServiceAccount(controlPlaneNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      name,
		},
	}
}

func KarpenterOperatorRole(controlPlaneNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      name,
		},
	}
}

func KarpenterOperatorRoleBinding(controlPlaneNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      name,
		},
	}
}

func karpenterOperatorSelector() map[string]string {
	return map[string]string{
		"app": "karpenter-operator",
	}
}

func ReconcileKarpenterOperatorDeployment(deployment *appsv1.Deployment,
	hcp *hyperv1.HostedControlPlane,
	sa *corev1.ServiceAccount,
	kubeConfigSecret *corev1.Secret,
	hypershiftOperatorImage string,
	controlPlaneOperatorImage string,
	karpenterProviderAWSImage string,
	setDefaultSecurityContext bool,
	ownerRef config.OwnerRef) error {

	// Preserve existing resource requirements.
	karpenterOperatorResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("60Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	operatorContainer := util.FindContainer(name, deployment.Spec.Template.Spec.Containers)
	if operatorContainer != nil {
		if len(operatorContainer.Resources.Requests) > 0 || len(operatorContainer.Resources.Limits) > 0 {
			karpenterOperatorResources = operatorContainer.Resources
		}
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: karpenterOperatorSelector(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: karpenterOperatorSelector(),
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
				TerminationGracePeriodSeconds: k8sutilspointer.To(int64(10)),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "target-kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  kubeConfigSecret.Name,
								DefaultMode: k8sutilspointer.To(int32(0640)),
								Items: []corev1.KeyToPath{
									{
										Key:  "kubeconfig",
										Path: "target-kubeconfig",
									},
								},
							},
						},
					},
				},
				Containers: []corev1.Container{},
			},
		},
	}

	mainContainer := corev1.Container{
		Resources:       karpenterOperatorResources,
		Name:            name,
		Image:           hypershiftOperatorImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "target-kubeconfig",
				MountPath: "/mnt/kubeconfig",
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
		Command: []string{
			"/usr/bin/karpenter-operator",
		},
		Args: []string{
			"--target-kubeconfig=/mnt/kubeconfig/target-kubeconfig",
			"--namespace=$(MY_NAMESPACE)",
			"--control-plane-operator-image=" + controlPlaneOperatorImage,
			"--karpenter-provider-aws-image=" + karpenterProviderAWSImage,
		},
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "serviceaccount-token",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: corev1.StorageMediumMemory,
					},
				},
			},
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "karpenter-credentials",
					},
				},
			})

		mainContainer.Env = append(mainContainer.Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: "/etc/provider/credentials",
			},
			corev1.EnvVar{
				Name:  "AWS_REGION",
				Value: hcp.Spec.Platform.AWS.Region,
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			})

		mainContainer.VolumeMounts = append(mainContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      "serviceaccount-token",
				MountPath: "/var/run/secrets/openshift/serviceaccount",
			},
			corev1.VolumeMount{
				Name:      "provider-creds",
				MountPath: "/etc/provider",
			})

		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:            "token-minter",
			Image:           controlPlaneOperatorImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/usr/bin/control-plane-operator", "token-minter"},
			Args: []string{
				"--service-account-namespace=kube-system",
				"--service-account-name=karpenter",
				"--token-file=/var/run/secrets/openshift/serviceaccount/token",
				fmt.Sprintf("--kubeconfig-secret-namespace=%s", deployment.Namespace),
				"--kubeconfig-secret-name=service-network-admin-kubeconfig",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("30Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "serviceaccount-token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
			},
		})
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, mainContainer)

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), controlPlaneOperatorImage, &deployment.Spec.Template.Spec)
	deploymentConfig := config.DeploymentConfig{
		AdditionalLabels: map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		},
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}

	replicas := k8sutilspointer.To(1)
	deploymentConfig.SetDefaults(hcp, nil, replicas)
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.ApplyTo(deployment)

	return nil
}

func ReconcileKarpenterOperatorRole(role *rbacv1.Role, owner config.OwnerRef) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{
				"get",
				"watch",
				"create",
			},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{
				"patch",
				"update",
			},
			ResourceNames: []string{
				"karpenter-leader-election",
			},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{
				"deployments",
			},
			Verbs: []string{
				"create",
				"update",
				"patch",
				"delete",
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{
				"secrets",
				"serviceaccounts",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{
				"hostedcontrolplanes",
				"hostedcontrolplanes/finalizers",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{
				"roles",
				"rolebindings",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"create",
				"update",
				"patch",
				"delete",
				"deletecollection",
			},
		},
	}
	return nil
}

func ReconcileKarpenterOperatorRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount, owner config.OwnerRef) error {
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

// ReconcileKarpenter orchestrates reconciliation of karpenter components.
func ReconcileKarpenterOperator(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, c client.Client, hypershiftOperatorImage, controlPlaneOperatorImage, karpenterProviderAWSImage string, hcp *hyperv1.HostedControlPlane) error {
	ownerRef := config.OwnerRefFrom(hcp)
	setDefaultSecurityContext := false

	awsCredentialsTemplate := `[default]
	role_arn = %s
	web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
	sts_regional_endpoints = regional
`
	arn := hcp.Spec.AutoNode.Provisioner.Karpenter.AWS.RoleARN
	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcp.Namespace,
			Name:      "karpenter-credentials",
		},
	}
	if _, err := createOrUpdate(ctx, c, credentialsSecret, func() error {
		credentials := fmt.Sprintf(awsCredentialsTemplate, arn)
		credentialsSecret.Data = map[string][]byte{"credentials": []byte(credentials)}
		credentialsSecret.Type = corev1.SecretTypeOpaque
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile karpenter credentials secret %s/%s: %w", credentialsSecret.Namespace, credentialsSecret.Name, err)
	}

	role := KarpenterOperatorRole(hcp.Namespace)
	_, err := createOrUpdate(ctx, c, role, func() error {
		return ReconcileKarpenterOperatorRole(role, ownerRef)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile karpenter role: %w", err)
	}

	serviceAccount := KarpenterOperatorServiceAccount(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, serviceAccount, func() error {
		util.EnsurePullSecret(serviceAccount, controlplaneoperator.PullSecret("").Name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile karpenter service account: %w", err)
	}

	roleBinding := KarpenterOperatorRoleBinding(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, roleBinding, func() error {
		return ReconcileKarpenterOperatorRoleBinding(roleBinding, role, serviceAccount, ownerRef)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile karpenter role binding: %w", err)
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig != nil {
		// Resolve the kubeconfig secret for HCCO which is used for karpeneter for convenience
		kubeConfigSecret := manifests.HCCOKubeconfigSecret(hcp.Namespace)
		err = c.Get(ctx, client.ObjectKeyFromObject(kubeConfigSecret), kubeConfigSecret)
		if err != nil {
			return fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", kubeConfigSecret.Name, err)
		}

		deployment := KarpenterOperatorDeployment(hcp.Namespace)
		_, err = createOrUpdate(ctx, c, deployment, func() error {
			return ReconcileKarpenterOperatorDeployment(deployment, hcp, serviceAccount, kubeConfigSecret, hypershiftOperatorImage, controlPlaneOperatorImage, karpenterProviderAWSImage, setDefaultSecurityContext, ownerRef)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile karpenter deployment: %w", err)
		}
	}

	return nil
}
