package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KASConnectionCheckerName is the name of the KAS connection checker DaemonSet
	KASConnectionCheckerName = "kas-connection-checker"
	// KASConnectionCheckerNamespace is the namespace where the DaemonSet is deployed
	KASConnectionCheckerNamespace = "kube-system"
)

// KASConnectionCheckerDaemonSet returns an empty DaemonSet object for the KAS connection checker
func KASConnectionCheckerDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}
