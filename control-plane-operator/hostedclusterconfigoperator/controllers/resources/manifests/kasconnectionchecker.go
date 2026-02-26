package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KASConnectionCheckerName is the name of the KAS connection checker DaemonSet
	KASConnectionCheckerName = "kas-connection-checker"
	// KASConnectionCheckerNamespace is the namespace where the DaemonSet is deployed
	KASConnectionCheckerNamespace = "kube-system"
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

// KASConnectionCheckerDaemonSet returns an empty DaemonSet object for the KAS connection checker
func KASConnectionCheckerDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}
