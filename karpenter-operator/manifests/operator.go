package manifests

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
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
		"karpenter": "karpenter",
	}
}

func ReconcileKarpenterOperatorDeployment(deployment *appsv1.Deployment,
	hcp *hyperv1.HostedControlPlane,
	sa *corev1.ServiceAccount,
	kubeConfigSecret *corev1.Secret,
	hypershiftOperatorImage string,
	controlPlaneOperatorImage string,
	setDefaultSecurityContext bool,
	ownerRef config.OwnerRef) error {

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
										Key:  "value",
										Path: "target-kubeconfig",
									},
								},
							},
						},
					},
					{
						Name: "serviceaccount-token",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            name,
						Image:           hypershiftOperatorImage,
						ImagePullPolicy: corev1.PullAlways,
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
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/healthz",
									Port:   intstr.FromString("http"),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 60,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    5,
							TimeoutSeconds:      5,
						},

						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/readyz",
									Port:   intstr.FromString("http"),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							PeriodSeconds:    10,
							SuccessThreshold: 1,
							FailureThreshold: 3,
							TimeoutSeconds:   5,
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "metrics",
								ContainerPort: 8000,
							},
							{
								Name:          "http",
								ContainerPort: 8081,
								Protocol:      corev1.ProtocolTCP,
							},
						},
					},
				},
			},
		},
	}

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
func ReconcileKarpenterOperator(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, c client.Client, hypershiftOperatorImage, controlPlaneOperatorImage string, hcp *hyperv1.HostedControlPlane) error {
	ownerRef := config.OwnerRefFrom(hcp)
	setDefaultSecurityContext := false

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
		// Resolve the kubeconfig secret for CAPI which is used for karpeneter for convenience
		capiKubeConfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      fmt.Sprintf("%s-kubeconfig", hcp.Spec.InfraID),
			},
		}
		err = c.Get(ctx, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret)
		if err != nil {
			return fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
		}

		deployment := KarpenterOperatorDeployment(hcp.Namespace)
		_, err = createOrUpdate(ctx, c, deployment, func() error {
			return ReconcileKarpenterOperatorDeployment(deployment, hcp, serviceAccount, capiKubeConfigSecret, hypershiftOperatorImage, controlPlaneOperatorImage, setDefaultSecurityContext, ownerRef)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile karpenter deployment: %w", err)
		}
	}

	return nil
}
