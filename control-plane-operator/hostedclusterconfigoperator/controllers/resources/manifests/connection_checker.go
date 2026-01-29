package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KASConnectionCheckerDSName    = "kas-connection-checker"
	KASConnectionCheckerNamespace = "kube-system"
)

func KASConnectionCheckerDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASConnectionCheckerDSName,
			Namespace: KASConnectionCheckerNamespace,
		},
	}
}
