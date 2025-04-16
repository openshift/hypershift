package karpenter

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/ptr"

	client "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	karpenterName = "karpenter"
)

func KarpenterDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      karpenterName,
		},
	}
}

func KarpenterServiceAccount(controlPlaneNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      karpenterName,
		},
	}
}

func KarpenterRole(controlPlaneNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      karpenterName,
		},
	}
}

func KarpenterRoleBinding(controlPlaneNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      karpenterName,
		},
	}
}

func karpenterSelector() map[string]string {
	return map[string]string{
		"app": karpenterName,
	}
}

func ReconcileKarpenterDeployment(deployment *appsv1.Deployment,
	hcp *hyperv1.HostedControlPlane,
	sa *corev1.ServiceAccount,
	kubeConfigSecret *corev1.Secret,
	availabilityProberImage, tokenMinterImage, karpenterProviderAWSImage string,
	setDefaultSecurityContext bool,
	ownerRef config.OwnerRef) error {

	// Preserve existing resource requirements.
	karpenterResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("60Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	mainContainer := util.FindContainer(karpenterName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			karpenterResources = mainContainer.Resources
		}
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: karpenterSelector(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: karpenterSelector(),
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
					{
						Name: "provider-creds",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: "karpenter-credentials",
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:      karpenterName,
						Resources: karpenterResources,
						// TODO(alberto): lifecycle this image.
						Image:           karpenterProviderAWSImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "target-kubeconfig",
								MountPath: "/mnt/kubeconfig",
							},
							{
								Name:      "provider-creds",
								MountPath: "/etc/provider",
							},
							{
								Name:      "serviceaccount-token",
								MountPath: "/var/run/secrets/openshift/serviceaccount",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name:  "KUBECONFIG",
								Value: "/mnt/kubeconfig/target-kubeconfig",
							},
							{
								Name: "SYSTEM_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
							{
								Name:  "DISABLE_WEBHOOK",
								Value: "true",
							},
							{
								Name:  "DISABLE_LEADER_ELECTION",
								Value: "true",
							},
							{
								Name:  "FEATURE_GATES",
								Value: "Drift=true",
							},
							{
								Name:  "AWS_REGION",
								Value: hcp.Spec.Platform.AWS.Region,
							},
							{
								Name:  "AWS_SHARED_CREDENTIALS_FILE",
								Value: "/etc/provider/credentials",
							},
							{
								Name:  "AWS_SDK_LOAD_CONFIG",
								Value: "true",
							},
							{
								Name:  "HEALTH_PROBE_PORT",
								Value: "8081",
							},
							// TODO (alberto): this is to satisfy current karpenter requirements. We should relax the req.
							{
								Name:  "CLUSTER_ENDPOINT",
								Value: "https://fake.com",
							},
							{
								Name:  "CLUSTER_NAME",
								Value: hcp.Spec.InfraID,
							},
						},
						// Command: []string{""},
						Args: []string{
							"--log-level=debug",
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
					{
						Name:    "token-minter",
						Command: []string{"/usr/bin/control-plane-operator", "token-minter"},
						Args: []string{
							"--service-account-namespace=kube-system",
							"--service-account-name=karpenter",
							"--token-file=/var/run/secrets/openshift/serviceaccount/token",
							"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("30Mi"),
							},
						},
						Image: tokenMinterImage,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "target-kubeconfig",
								MountPath: "/mnt/kubeconfig",
							},
							{
								Name:      "serviceaccount-token",
								MountPath: "/var/run/secrets/openshift/serviceaccount",
							},
						},
					},
				},
			},
		},
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage, &deployment.Spec.Template.Spec)
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

func ReconcileKarpenterRole(role *rbacv1.Role, owner config.OwnerRef) error {
	owner.ApplyTo(role)
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
	}
	return nil
}

func ReconcileKarpenterRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount, owner config.OwnerRef) error {
	owner.ApplyTo(binding)
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
func (r *Reconciler) reconcileKarpenter(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	createOrUpdate := r.CreateOrUpdate
	c := r.ManagementClient
	ownerRef := config.OwnerRefFrom(hcp)
	setDefaultSecurityContext := false
	availabilityProberImage := r.ControlPlaneOperatorImage
	tokenMinterImage := r.ControlPlaneOperatorImage
	karpenterProviderAWSImage, exists := hcp.Annotations[hyperkarpenterv1.KarpenterProviderAWSImage]
	if !exists {
		karpenterProviderAWSImage = "public.ecr.aws/karpenter/controller:1.0.7"
	}

	role := KarpenterRole(hcp.Namespace)
	_, err := createOrUpdate(ctx, c, role, func() error {
		return ReconcileKarpenterRole(role, ownerRef)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile karpenter role: %w", err)
	}

	serviceAccount := KarpenterServiceAccount(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, serviceAccount, func() error {
		util.EnsurePullSecret(serviceAccount, controlplaneoperator.PullSecret("").Name)
		ownerRef.ApplyTo(serviceAccount)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile karpenter service account: %w", err)
	}

	roleBinding := KarpenterRoleBinding(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, roleBinding, func() error {
		return ReconcileKarpenterRoleBinding(roleBinding, role, serviceAccount, ownerRef)
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

		deployment := KarpenterDeployment(hcp.Namespace)
		_, err = createOrUpdate(ctx, c, deployment, func() error {
			return ReconcileKarpenterDeployment(deployment, hcp, serviceAccount, capiKubeConfigSecret, availabilityProberImage, tokenMinterImage, karpenterProviderAWSImage, setDefaultSecurityContext, ownerRef)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile karpenter deployment: %w", err)
		}
	}

	return nil
}
