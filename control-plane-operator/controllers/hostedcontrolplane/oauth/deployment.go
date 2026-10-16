package oauth

import (
	corev1 "k8s.io/api/core/v1"
)

func oauthContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "oauth-server",
	}
}
