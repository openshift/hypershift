package autoscaler

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"
)

type Deployment struct {
	Namespace      *corev1.Namespace
	ServiceAccount *corev1.ServiceAccount
	Image          string
}

func (o Deployment) Build() *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-autoscaler",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: k8sutilspointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "cluster-autoscaler",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "cluster-autoscaler",
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
							Name: "target-kubeconfig",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									// TODO: this should come from HCP status
									SecretName: "admin-kubeconfig",
									Items: []corev1.KeyToPath{
										{
											// TODO: this should come from HCP status
											Key:  "value",
											Path: "target-kubeconfig",
										},
									},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "cluster-autoscaler",
							Image:           o.Image,
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
							Command: []string{"/cluster-autoscaler"},
							Args: []string{
								"--cloud-provider=clusterapi",
								"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
								"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
								"--clusterapi-cloud-config-authoritative",
								"--alsologtostderr",
								"--v=4",
							},
						},
					},
				},
			},
		},
	}
	return deployment
}

type ServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o ServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "cluster-autoscaler",
		},
	}
	return sa
}

type Role struct {
	Namespace *corev1.Namespace
}

func (o Role) Build() *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "cluster-autoscaler-management",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"cluster.x-k8s.io"},
				Resources: []string{
					"machinedeployments",
					"machinedeployments/scale",
					"machines",
					"machinesets",
					"machinesets/scale",
				},
				Verbs: []string{"*"},
			},
		},
	}
	return role
}

type RoleBinding struct {
	Role           *rbacv1.Role
	ServiceAccount *corev1.ServiceAccount
}

func (o RoleBinding) Build() *rbacv1.RoleBinding {
	binding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Role.Namespace,
			Name:      "cluster-autoscaler-management",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     o.Role.Name,
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
