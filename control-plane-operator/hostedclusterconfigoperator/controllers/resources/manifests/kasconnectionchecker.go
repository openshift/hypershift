package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KASConnectionCheckerName is the name of the KAS connection checker Deployment
	KASConnectionCheckerName = "kas-connection-checker"
	// KASConnectionCheckerNamespace is the namespace where the Deployment is deployed
	KASConnectionCheckerNamespace = "kube-system"
	// KASConnectionCheckerConfigMapName is the name of the ConfigMap used to report connectivity check results
	KASConnectionCheckerConfigMapName = "control-plane-connectivity-check"
)

// KASConnectionCheckerServiceAccount returns a ServiceAccount for the KAS connection checker
func KASConnectionCheckerServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}

// KASConnectionCheckerDeployment returns an empty Deployment object for the KAS connection checker
func KASConnectionCheckerDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}

// KASConnectionCheckerConfigMap returns an empty ConfigMap for reporting connectivity check results
func KASConnectionCheckerConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerConfigMapName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}

// KASConnectionCheckerRole returns a Role for the KAS connection checker
func KASConnectionCheckerRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}

// KASConnectionCheckerRoleBinding returns a RoleBinding for the KAS connection checker
func KASConnectionCheckerRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}
