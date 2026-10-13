package oauth

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	KubeadminSecretHashAnnotation = "hypershift.openshift.io/kubeadmin-secret-hash"
)

func oauthContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "oauth-server",
	}
}
