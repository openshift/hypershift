package csidriveroperator

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ServiceAccountName string = "kubevirt-csi-driver-operator"
	RoleName string = "kubevirt-csi-driver-operator-role"
	ClusterRoleName string = "kubevirt-csi-driver-operator-clusterrole"
	ImagePath string = "quay.io/ydayagi/csi-driver-operator:latest"
)

func OperatorDeployment(controlPlaneOperatorNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-operator",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "kubevirt-csi-driver-operator",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "kubevirt-csi-driver-operator",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: ServiceAccountName,
					PriorityClassName: "system-cluster-critical",
					Tolerations: []corev1.Toleration{
						{
							Key: "CriticalAddonsOnly",
							Operator: "Exists",
						},
					},
					Containers: []corev1.Container{
						{
							Name: "kubevirt-csi-driver-operator",
							ImagePullPolicy: "Always",
							Image: ImagePath,
							Args: []string{"start"},
							Env: []corev1.EnvVar{
								{
									Name: "OPERATOR_NAME",
									Value: "kubevirt-csi-driver-operator",
								},
								{
									Name: "DRIVER_IMAGE",
									Value: "quay.io/ydayagi/csi-driver:latest",
								},
								{
									Name: "PROVISIONER_IMAGE",
									Value: "quay.io/openshift/origin-csi-external-provisioner:latest",
								},
								{
									Name: "ATTACHER_IMAGE",
									Value: "quay.io/openshift/origin-csi-external-attacher:latest",
								},
								{
									Name: "NODE_DRIVER_REGISTRAR_IMAGE",
									Value: "quay.io/openshift/origin-csi-node-driver-registrar:latest",
								},
								{
									Name: "LIVENESS_PROBE_IMAGE",
									Value: "quay.io/openshift/origin-csi-livenessprobe:latest",
								},
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func OperatorServiceAccount(controlPlaneOperatorNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      ServiceAccountName,
		},
	}
}

func ControllerServiceAccount(controlPlaneOperatorNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "admin",
		},
	}
}

func ControllerAdminRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "admin-clusterrolebinding",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "admin",
				Namespace: controlPlaneOperatorNamespace,
			},
		},
	}
}

func OperatorRole(controlPlaneOperatorNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      RoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods", "services", "endpoints",
					"persistentvolumeclaims", "events", "configmaps", "secrets"},
				Verbs: []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"get"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "daemonsets", "replicasets", "statefulsets"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"monitoring.coreos.com"},
				Resources: []string{"servicemonitors"},
				Verbs:     []string{"get", "create"},
			},
		},
	}
}

func OperatorRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "kubevirt-csi-driver-operator-rolebinding",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     RoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      ServiceAccountName,
				Namespace: controlPlaneOperatorNamespace,
			},
		},
	}
}

func OperatorClusterRole(controlPlaneOperatorNamespace string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      ClusterRoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{"security.openshift.io"},
				ResourceNames: []string{"privileged"},
				Resources:     []string{"privileged"},
				Verbs:         []string{"use"},
			},
			{
				APIGroups:     []string{""},
				ResourceNames: []string{"extension-apiserver-authentication", "kubevirt-csi-driver-operator-lock"},
				Resources:     []string{"configmaps"},
				Verbs:         []string{"*"},
			},
			{
				APIGroups: []string{"rbac.authorization.k8s.io"},
				Resources: []string{"clusterroles", "clusterrolebindings", "roles", "rolebindings"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"serviceaccounts"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{"apiextensions.k8s.io"},
				Resources: []string{"customresourcedefinitions"},
				Verbs:     []string{"list", "create", "watch", "delete"},
			},
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims/status"},
				Verbs:     []string{"patch", "update"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "daemonsets", "replicasets", "statefulsets"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"volumeattachments"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotcontents/status", "volumesnapshots/status"},
				Verbs:     []string{"update", "patch"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"storageclasses", "csinodes"},
				Verbs:     []string{"create", "get", "list", "watch", "update", "delete"},
			},
			{
				APIGroups: []string{"*"},
				Resources: []string{"events"},
				Verbs:     []string{"watch", "list", "get", "create", "delete", "patch", "update"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotclasses"},
				Verbs:     []string{"create", "get", "list", "watch", "update", "delete"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshotcontents"},
				Verbs:     []string{"create", "get", "list", "watch", "update", "delete"},
			},
			{
				APIGroups: []string{"snapshot.storage.k8s.io"},
				Resources: []string{"volumesnapshots"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{"storage.k8s.io"},
				Resources: []string{"csidrivers"},
				Verbs:     []string{"create", "get", "list", "watch", "update", "delete"},
			},
			{
				APIGroups: []string{"csi.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"cloudcredential.openshift.io"},
				Resources: []string{"credentialsrequests"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"config.openshift.io"},
				Resources: []string{"infrastructures"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"clustercsidrivers", "clustercsidrivers/status"},
				Verbs:     []string{"get", "list", "watch", "update"},
			},
			{
				APIGroups: []string{"csi.storage.k8s.io"},
				Resources: []string{"csinodeinfos"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"csi.storage.k8s.io"},
				Resources: []string{"csidrivers"},
				Verbs:     []string{"get", "list", "watch", "update", "create"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get"},
			},
			{
				APIGroups: []string{"machine.openshift.io"},
				Resources: []string{"machinesets"},
				Verbs:     []string{"list"},
			},
		},
	}
}

func OperatorClusterRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "kubevirt-csi-driver-operator-clusterrolebinding",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     ClusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      ServiceAccountName,
				Namespace: controlPlaneOperatorNamespace,
			},
		},
	}
}