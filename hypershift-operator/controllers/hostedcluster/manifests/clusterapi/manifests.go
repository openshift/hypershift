package clusterapi

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
)

type ManagerDeployment struct {
	Namespace      *corev1.Namespace
	Image          string
	ServiceAccount *corev1.ServiceAccount
}

func (o ManagerDeployment) Build() *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-api",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: k8sutilspointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "cluster-api",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "cluster-api",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name:            "manager",
							Image:           o.Image,
							ImagePullPolicy: corev1.PullAlways,
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
							Args:    []string{"--namespace", "$(MY_NAMESPACE)", "--alsologtostderr", "--v=4"},
						},
					},
				},
			},
		},
	}
	return deployment
}

type ManagerServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o ManagerServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "cluster-api",
		},
	}
	return sa
}

type ManagerClusterRole struct{}

func (o ManagerClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-api",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"get", "list", "watch"},
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
				Resources: []string{
					"hostedcontrolplanes",
					"hostedcontrolplanes/status",
					"externalinfraclusters",
					"externalinfraclusters/status",
				},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{
					"configmaps",
					"events",
					"nodes",
					"secrets",
				},
				Verbs: []string{"*"},
			},
		},
	}
	return role
}

type ManagerClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
}

func (o ManagerClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-api",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}

type AWSProviderDeployment struct {
	Namespace           *corev1.Namespace
	Image               string
	ServiceAccount      *corev1.ServiceAccount
	ProviderCredentials *corev1.Secret
}

func (o AWSProviderDeployment) Build() *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capa-controller-manager",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: k8sutilspointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"control-plane": "capa-controller-manager",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"control-plane": "capa-controller-manager",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            o.ServiceAccount.Name,
					TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
					Tolerations: []corev1.Toleration{
						{
							Key:    "node-role.kubernetes.io/master",
							Effect: corev1.TaintEffectNoSchedule,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "credentials",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: o.ProviderCredentials.Name,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "manager",
							Image:           o.Image,
							ImagePullPolicy: corev1.PullAlways,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "credentials",
									MountPath: "/home/.aws",
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
							Args:    []string{"--namespace", "$(MY_NAMESPACE)", "--alsologtostderr", "--v=4"},
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
		},
	}
	return deployment
}

type AWSProviderServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o AWSProviderServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "capa-controller-manager",
		},
	}
	return sa
}

type AWSProviderClusterRole struct{}

func (o AWSProviderClusterRole) Build() *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRole",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "capa-manager-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{
					"events",
					"secrets",
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
		},
	}
	return role
}

type AWSProviderClusterRoleBinding struct {
	ClusterRole    *rbacv1.ClusterRole
	ServiceAccount *corev1.ServiceAccount
}

func (o AWSProviderClusterRoleBinding) Build() *rbacv1.ClusterRoleBinding {
	binding := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "capa-manager-rolebinding",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.ClusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}
