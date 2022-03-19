package manifests

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", hostedClusterNamespace, strings.ReplaceAll(hostedClusterName, ".", "-")),
		},
	}
}

func KubeConfigSecret(hostedClusterNamespace, hostedClusterName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      hostedClusterName + "-admin-kubeconfig",
		},
	}
}

func KubeadminPasswordSecret(hostedClusterNamespace, hostedClusterName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      hostedClusterName + "-kubeadmin-password",
		},
	}
}
