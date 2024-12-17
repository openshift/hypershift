package controlplaneoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	ServiceSignerPrivateKey = "service-account.key"
	ServiceSignerPublicKey  = "service-account.pub"
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

func OperatorIngressRole(ingressNamespace string, controlPlaneOperatorNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingressNamespace,
			Name:      "control-plane-operator-" + controlPlaneOperatorNamespace,
		},
	}
}

func OperatorIngressRoleBinding(ingressNamespace string, controlPlaneOperatorNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingressNamespace,
			Name:      "control-plane-operator-" + controlPlaneOperatorNamespace,
		},
	}
}

func OperatorIngressOperatorRole(ingressOperatorNamespace string, controlPlaneOperatorNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingressOperatorNamespace,
			Name:      "control-plane-operator-" + controlPlaneOperatorNamespace,
		},
	}
}

func OperatorIngressOperatorRoleBinding(ingressOperatorNamespace string, controlPlaneOperatorNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingressOperatorNamespace,
			Name:      "control-plane-operator-" + controlPlaneOperatorNamespace,
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

func PullSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "pull-secret",
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

func UserCABundle(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-ca-bundle",
			Namespace: controlPlaneNamespace,
		},
	}
}

func PodMonitor(controlPlaneNamespace string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "controlplane-operator",
		},
	}
}

func ServiceAccountSigningKeySecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-signing-key",
			Namespace: ns,
		},
	}
}

func OIDCCAConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oidc-ca",
			Namespace: ns,
		},
	}
}
