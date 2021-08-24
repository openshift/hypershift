package controlplaneoperator

import (
	capiawsv1 "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha4"
	capiibmv1 "github.com/openshift/hypershift/thirdparty/clusterapiprovideribmcloud/v1alpha4"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
)

func OperatorDeployment(controlPlaneOperatorNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-operator",
		},
	}
}

func OperatorServiceAccount(controlPlaneOperatorNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-operator",
		},
	}
}

func OperatorClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "control-plane-operator",
		},
	}
}

func OperatorClusterRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "control-plane-operator-" + controlPlaneOperatorNamespace,
		},
	}
}

func OperatorRole(controlPlaneOperatorNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-operator",
		},
	}
}

func OperatorRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-operator",
		},
	}
}

func CAPICluster(controlPlaneOperatorNamespace string, infraID string) *capiv1.Cluster {
	return &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      infraID,
		},
	}
}

func HostedControlPlane(controlPlaneNamespace string, hostedClusterName string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hostedClusterName,
		},
	}
}

func AWSCluster(controlPlaneNamespace string, hostedClusterName string) *capiawsv1.AWSCluster {
	return &capiawsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hostedClusterName,
		},
	}
}

func PullSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "pull-secret",
		},
	}
}

func SigningKey(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "signing-key",
		},
	}
}

func SSHKey(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "ssh-key",
		},
	}
}

func IBMCloudCluster(controlPlaneNamespace string, hostedClusterName string) *capiibmv1.IBMCluster {
	return &capiibmv1.IBMCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hostedClusterName,
		},
	}
}
