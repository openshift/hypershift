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

func oauthContainerHTTPProxy() *corev1.Container {
	return &corev1.Container{
		Name: "http-proxy",
	}
}

func oauthContainerSocks5Proxy() *corev1.Container {
	return &corev1.Container{
		Name: "socks5-proxy",
	}
}
