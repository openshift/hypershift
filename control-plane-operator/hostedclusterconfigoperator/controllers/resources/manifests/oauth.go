package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"
)

func OAuthCABundle() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-serving-cert",
			Namespace: "openshift-config-managed",
		},
	}
}

func OpenShiftOAuthServerCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-server-crt",
			Namespace: ns,
		},
	}
}

func OAuthServerChallengingClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-challenging-client",
		},
	}
}

func OAuthServerBrowserClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-browser-client",
		},
	}
}
